# cms-image

CMS Edge 使用的容器映像的建置與發布。

第一個映像是衍生的 cloudflared：官方 cloudflared 加上一個很小的健康檢查探針，發布為公開的 GHCR 套件，edge 拉取不需要憑證。

狀態：建置中。內容（探針原始碼、Dockerfile、發布 workflow）會以 PR 加入。

所有變更都走分支與 PR，不直接推 `main`。
