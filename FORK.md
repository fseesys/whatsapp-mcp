# Fork notes (fseesys/whatsapp-mcp)

Fork of [felipeadeildo/whatsapp-mcp](https://github.com/felipeadeildo/whatsapp-mcp) for **agent-whatsapp** ingest.

- **This fork:** https://github.com/fseesys/whatsapp-mcp  
- **Upstream:** https://github.com/felipeadeildo/whatsapp-mcp  

GPL-3.0. Do not copy this Go tree into `wa-web-bot` or `wa-stack`.

## Patches vs upstream

- Live media download is **synchronous** before the webhook so `file_path` is set (CDN expires).
- Files land in `data/media/<chat-jid>/YYYY/MM/DD/<msgid>-<name>` (`TIMEZONE`, default HKT). Chat folder is the durable JID with `@`/`.`/`:` → `_` (1:1 `852…_s.whatsapp.net`, group `…_g.us`).
- Webhook includes `file_path`, `download_status`, `reply_to_id`.
- Default auto-download types: image, audio, ptt, sticker, video, document, gif. Max 50MB.
- Quoted/reply stanza id on normal messages, not only reactions.

## Remotes (this PC)

```powershell
cd D:\dev\whatsapp-mcp
git remote -v
# origin   → https://github.com/fseesys/whatsapp-mcp.git
# upstream → https://github.com/felipeadeildo/whatsapp-mcp.git
```

## Run

Prefer native Go (pure Go sqlite via `modernc.org/sqlite`; no Docker required):

```powershell
cd D:\dev\whatsapp-mcp
copy .env.example .env   # set MCP_API_KEY
go mod download
go build -o whatsapp-mcp.exe .
.\whatsapp-mcp.exe
```

Pair with [wa-agent](https://github.com/fseesys/wa-agent) filer webhook. Stack install: [wa-stack](https://github.com/fseesys/wa-stack). New-account run-in: `wa-web-bot/product.md`. Home/residential IP, not a VPS QR.
