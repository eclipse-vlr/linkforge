#requires -Version 7.2
[CmdletBinding()]
param(
    [ValidateRange(1024, 65535)]
    [int]$Port = 18081
)

$ErrorActionPreference = 'Stop'
$repository = Split-Path -Parent $PSScriptRoot
$project = 'linkforge-smoke-' + [guid]::NewGuid().ToString('N').Substring(0, 8)
$baseURL = "http://127.0.0.1:$Port"
$apiKey = 'demo-only-' + [guid]::NewGuid().ToString('N')
$temporary = Join-Path ([IO.Path]::GetTempPath()) $project
[IO.Directory]::CreateDirectory($temporary) | Out-Null
$overrideFile = Join-Path $temporary 'compose.yml'
$override = @"
services:
  api:
    image: $project-api
    ports: !override
      - "127.0.0.1:${Port}:8080"
    environment:
      BASE_URL: "$baseURL"
      CREATE_LINK_API_KEY: "$apiKey"
  db:
    container_name: $project-db
    ports: !reset []
  redis:
    ports: !reset []
"@
[IO.File]::WriteAllText($overrideFile, $override)
$compose = @('compose', '--project-name', $project, '-f', (Join-Path $repository 'docker-compose.yml'), '-f', $overrideFile)
$validated = $false
$handler = [Net.Http.HttpClientHandler]::new()
$handler.AllowAutoRedirect = $false
$handler.UseProxy = $false
$client = [Net.Http.HttpClient]::new($handler)
$client.Timeout = [TimeSpan]::FromSeconds(5)

function Invoke-DemoDocker {
    param([string[]]$Arguments)
    $result = & docker @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Docker command failed: $($Arguments -join ' ')" }
    return $result
}

function Invoke-DemoRequest {
    param([string]$Method, [string]$Path, [int]$Expected, [string]$Body = '', [switch]$WithoutKey)
    $request = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::new($Method), $baseURL + $Path)
    if (-not $WithoutKey) { $request.Headers.Add('X-API-Key', $apiKey) }
    if ($Body) { $request.Content = [Net.Http.StringContent]::new($Body, [Text.Encoding]::UTF8, 'application/json') }
    $response = $null
    try {
        $response = $client.SendAsync($request).GetAwaiter().GetResult()
        $content = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        if ([int]$response.StatusCode -ne $Expected) { throw "$Method $Path returned $([int]$response.StatusCode), expected ${Expected}: $content" }
        return [pscustomobject]@{ Body = $content; Location = [string]$response.Headers.Location }
    } finally {
        if ($response) { $response.Dispose() }
        $request.Dispose()
    }
}

try {
    $probe = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $Port)
    try { $probe.Start() } finally { $probe.Stop() }
    $config = ((Invoke-DemoDocker -Arguments ($compose + @('config', '--format', 'json'))) -join "`n") | ConvertFrom-Json
    $volume = $project + '_postgres_data'
    if ($config.volumes.postgres_data.name -ne $volume -or
        $config.services.db.container_name -ne "$project-db" -or
        $config.networks.default.name -ne "${project}_default" -or
        $config.services.db.ports -or $config.services.redis.ports) {
        throw 'The demo configuration is not isolated from other projects.'
    }
    $validated = $true
    Write-Host "1. Starting isolated smoke test at $baseURL" -ForegroundColor Cyan
    Invoke-DemoDocker -Arguments ($compose + @('up', '-d', '--build')) | Out-Null
    $ready = $false
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    while ([DateTime]::UtcNow -lt $deadline) {
        try {
            Invoke-DemoRequest -Method GET -Path '/healthz' -Expected 200 | Out-Null
            $ready = $true
            break
        } catch { Start-Sleep -Milliseconds 300 }
    }
    if (-not $ready) { throw 'The demo API did not become ready.' }

    Write-Host '2. API key and URL validation: expected 401 and 400' -ForegroundColor Cyan
    Invoke-DemoRequest -Method POST -Path '/api/v1/links' -Expected 401 -Body '{"url":"https://example.com/"}' -WithoutKey | Out-Null
    Invoke-DemoRequest -Method POST -Path '/api/v1/links' -Expected 400 -Body '{"url":"ftp://example.com/"}' | Out-Null

    Write-Host '3. Creating a link: expected 201 and initial click_count=0' -ForegroundColor Cyan
    $created = (Invoke-DemoRequest -Method POST -Path '/api/v1/links' -Expected 201 -Body '{"url":"https://example.com/"}').Body | ConvertFrom-Json
    if ($created.code -notmatch '^[a-zA-Z0-9]{8}$' -or $created.short_url -ne "$baseURL/$($created.code)") { throw 'Unexpected create response.' }
    $created | ConvertTo-Json
    $statsPath = "/api/v1/links/$($created.code)/stats"
    $stats = (Invoke-DemoRequest -Method GET -Path $statsPath -Expected 200).Body | ConvertFrom-Json
    if ($stats.click_count -ne 0) { throw 'The initial count must be zero.' }

    Write-Host '4. Two redirects: expected 302, correct Location and click_count=2' -ForegroundColor Cyan
    foreach ($attempt in 1..2) {
        $redirect = Invoke-DemoRequest -Method GET -Path "/$($created.code)" -Expected 302
        if ($redirect.Location -ne $created.original_url) { throw 'Unexpected redirect Location.' }
    }
    $stats = (Invoke-DemoRequest -Method GET -Path $statsPath -Expected 200).Body | ConvertFrom-Json
    if ($stats.click_count -ne 2) { throw 'Two redirects must be counted.' }
    $cached = Invoke-DemoDocker -Arguments ($compose + @('exec', '-T', 'redis', 'redis-cli', 'GET', "linkforge:link:$($created.code)"))
    $ttl = Invoke-DemoDocker -Arguments ($compose + @('exec', '-T', 'redis', 'redis-cli', 'PTTL', "linkforge:link:$($created.code)"))
    if ($cached -ne $created.original_url -or [long]$ttl -le 0) { throw 'The URL must be cached with a TTL.' }
    Write-Host "Redis contains the URL; remaining TTL: $ttl ms"

    Write-Host '5. Stopping only the test Redis: redirect remains 302, click_count=3' -ForegroundColor Cyan
    Invoke-DemoDocker -Arguments ($compose + @('stop', 'redis')) | Out-Null
    $redirect = Invoke-DemoRequest -Method GET -Path "/$($created.code)" -Expected 302
    $stats = (Invoke-DemoRequest -Method GET -Path $statsPath -Expected 200).Body | ConvertFrom-Json
    if ($redirect.Location -ne $created.original_url -or $stats.click_count -ne 3) { throw 'Redis failure broke the redirect or counter.' }
    $stats | ConvertTo-Json

    Write-Host '6. Graceful shutdown: expected exit code 0 and HTTP server stopped' -ForegroundColor Cyan
    $apiContainer = [string](Invoke-DemoDocker -Arguments ($compose + @('ps', '-q', 'api')))
    Invoke-DemoDocker -Arguments ($compose + @('stop', 'api')) | Out-Null
    $exitCode = Invoke-DemoDocker -Arguments @('inspect', '--format', '{{.State.ExitCode}}', $apiContainer.Trim())
    $logs = (Invoke-DemoDocker -Arguments ($compose + @('logs', '--no-color', 'api'))) -join "`n"
    if ($exitCode -ne '0' -or $logs -notmatch 'HTTP server stopped') { throw 'Graceful shutdown was not confirmed.' }
    $logs -split "`n" | Select-Object -Last 8 | Write-Host
    Write-Host 'PASS: all six smoke-test steps completed.' -ForegroundColor Green
} catch {
    if ($validated) { & docker @compose logs --no-color api 2>$null }
    throw
} finally {
    $client.Dispose()
    if ($validated) {
        $owner = & docker volume inspect --format '{{index .Labels "com.docker.compose.project"}}' $volume 2>$null
        if ($LASTEXITCODE -eq 0 -and $owner -ne $project) { throw 'Refusing to clean a volume outside the demo project.' }
        & docker @compose down --volumes
        if ($LASTEXITCODE -ne 0) { throw "Demo cleanup failed. Project: $project; override: $overrideFile" }
        $ownedImage = "${project}-api:latest"
        & docker image inspect $ownedImage *> $null
        if ($LASTEXITCODE -eq 0) {
            & docker image rm $ownedImage | Out-Null
            if ($LASTEXITCODE -ne 0) { throw "Smoke-test image cleanup failed: $ownedImage" }
        }
    }
    Remove-Item -LiteralPath $overrideFile
    if (@(Get-ChildItem -LiteralPath $temporary -Force).Count -eq 0) {
        [IO.Directory]::Delete($temporary)
    }
}
