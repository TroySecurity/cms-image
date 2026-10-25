# cms-image

CMS Edge 使用的容器映像的建置與發布。

## cloudflared 衍生映像

官方 cloudflared（以 digest 釘住）加上一個靜態的健康檢查探針 `/probe`，沒有其他改動。cloudflared 官方映像是 distroless（沒有 shell 與 curl），所以探針是一支靜態 Go 程式。

```
ghcr.io/troysecurity/cms-edge-cloudflared@sha256:<digest>
```

公開套件，拉取不需要登入。使用端應以 digest 釘住，不要用 tag。映像不含任何機敏資料。

### 探針做什麼

`/probe [addr]`（預設 `127.0.0.1:20241`，也就是 cloudflared 的 `--metrics` 位址）：

1. 讀 `/ready` 與 `/metrics`（逾時 2 秒，大小有上限）。
2. 輸出**一行** JSON（約 500 B，上限 1 KB）。
3. `/ready` 回 200 時 exit 0，否則 exit 1。因此 Docker 的健康狀態就是 cloudflared 的 readiness，`docker inspect` 的 `State.Health.Log[-1].Output` 帶有這一行。

使用方式（例）：

```
healthcheck:
  test: ["CMD", "/probe"]
  interval: 30s
  timeout: 5s
  retries: 3
```

### 輸出契約（`v` = 1）

| 欄位 | 說明 |
|---|---|
| `v` | 輸出格式版本。讀取端遇到不認得的版本應拒絕，不要猜 |
| `seq`、`ts` | 遞增序號與產生時間 |
| `ok` | `/ready` 是否為 200 |
| `readyStatus` | `/ready` 的 HTTP 狀態 |
| `readyConnections` | `/ready` 回報的連線數；**連線是否正常以此為準** |
| `connectorId` | cloudflared 的 connector id |
| `tunnel.totalRequests`、`requestErrors`、`concurrentRequests` | tunnel 層的請求數、錯誤數、同時請求數（`cloudflared_tunnel_total_requests`、`…_request_errors`、`…_concurrent_requests_per_tunnel`） |
| `tunnel.haConnections` | `cloudflared_tunnel_ha_connections`；**斷網時仍可能是 1 到 3，不能當作連線判斷** |
| `tunnel.responseByCode` | 依狀態碼的請求數（首個請求之後才有） |
| `serverLocations`、`err` | 連線的 Cloudflare 資料中心；錯誤訊息 |

指標名稱以 cloudflared 2026.9.3 對真連線的 tunnel 驗證過。升級 cloudflared 時要重新確認。

升版規則：新增欄位不改 `v`；移除或改變語意的欄位才升 `v`，且要先讓讀取端能解析新版本，再發布新映像。

### 建置與發布

- Pull request：測試並建置（`linux/amd64`、`linux/arm64`），不推送。
- 推送標籤 `cloudflared-<cloudflared 版本>-probe<N>`（例：`cloudflared-2026.9.3-probe1`）：測試、建置、推到 GHCR，並在 workflow 的摘要列出要釘住的 `repo@sha256:…`。
- 第一次發布後，要在 GitHub 套件設定把 `cms-edge-cloudflared` 設為 **Public** 並連結此 repo。
- 升級 cloudflared：改 `Dockerfile` 最後一個 `FROM` 的 digest，更新標籤中的版本。

### 本機

```
go vet ./... && go test ./...
docker build --platform linux/amd64 -t cms-edge-cloudflared:local .
```

## 規則

所有變更都走分支與 PR，不直接推 `main`。
