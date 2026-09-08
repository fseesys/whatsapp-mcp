package whatsapp

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"whatsapp-mcp/storage"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
)

// getInitialDownloadStatus determines the initial download status based on auto-download configuration.
func (c *Client) getInitialDownloadStatus(mediaType string, fileSize int64, fromHistory bool) string {
	// check if auto-download is enabled based on source
	if fromHistory && !c.mediaConfig.AutoDownloadFromHistory {
		return "skipped"
	}

	if c.shouldAutoDownload(mediaType, fileSize) {
		return "pending"
	}
	return "skipped"
}

// extractMediaMetadata extracts metadata from a WhatsApp message and returns media information.
func (c *Client) extractMediaMetadata(msg *waE2E.Message, messageID string, fromHistory bool) *storage.MediaMetadata {
	if msg == nil {
		return nil
	}

	// sticker message
	if sticker := msg.GetStickerMessage(); sticker != nil {
		fileName := fmt.Sprintf("sticker_%s.webp", messageID[:8])
		fileSize := int64(sticker.GetFileLength())

		return &storage.MediaMetadata{
			MessageID:      messageID,
			FileName:       fileName,
			FileSize:       fileSize,
			MimeType:       sticker.GetMimetype(),
			Width:          intPtr(int(sticker.GetWidth())),
			Height:         intPtr(int(sticker.GetHeight())),
			MediaKey:       sticker.GetMediaKey(),
			DirectPath:     sticker.GetDirectPath(),
			FileSHA256:     sticker.GetFileSHA256(),
			FileEncSHA256:  sticker.GetFileEncSHA256(),
			DownloadStatus: c.getInitialDownloadStatus("sticker", fileSize, fromHistory),
		}
	}

	// image message
	if img := msg.GetImageMessage(); img != nil {
		// * images don't have filename field, generate from message ID
		fileName := fmt.Sprintf("image_%s.jpg", messageID[:min(8, len(messageID))])
		fileSize := int64(img.GetFileLength())

		return &storage.MediaMetadata{
			MessageID:      messageID,
			FileName:       fileName,
			FileSize:       fileSize,
			MimeType:       img.GetMimetype(),
			Width:          intPtr(int(img.GetWidth())),
			Height:         intPtr(int(img.GetHeight())),
			MediaKey:       img.GetMediaKey(),
			DirectPath:     img.GetDirectPath(),
			FileSHA256:     img.GetFileSHA256(),
			FileEncSHA256:  img.GetFileEncSHA256(),
			DownloadStatus: c.getInitialDownloadStatus("image", fileSize, fromHistory),
		}
	}

	// video message
	if vid := msg.GetVideoMessage(); vid != nil {
		// * videos don't have filename field, generate from message ID
		fileName := fmt.Sprintf("video_%s.mp4", messageID[:min(8, len(messageID))])
		fileSize := int64(vid.GetFileLength())

		return &storage.MediaMetadata{
			MessageID:      messageID,
			FileName:       fileName,
			FileSize:       fileSize,
			MimeType:       vid.GetMimetype(),
			Width:          intPtr(int(vid.GetWidth())),
			Height:         intPtr(int(vid.GetHeight())),
			Duration:       intPtr(int(vid.GetSeconds())),
			MediaKey:       vid.GetMediaKey(),
			DirectPath:     vid.GetDirectPath(),
			FileSHA256:     vid.GetFileSHA256(),
			FileEncSHA256:  vid.GetFileEncSHA256(),
			DownloadStatus: c.getInitialDownloadStatus("video", fileSize, fromHistory),
		}
	}

	// audio message
	if aud := msg.GetAudioMessage(); aud != nil {
		fileName := "audio.ogg"
		mediaType := "audio"
		if aud.GetPTT() {
			fileName = "voice_note.ogg"
			mediaType = "ptt"
		}
		fileName = fmt.Sprintf("%s_%s", messageID[:8], fileName)
		fileSize := int64(aud.GetFileLength())

		return &storage.MediaMetadata{
			MessageID:      messageID,
			FileName:       fileName,
			FileSize:       fileSize,
			MimeType:       aud.GetMimetype(),
			Duration:       intPtr(int(aud.GetSeconds())),
			MediaKey:       aud.GetMediaKey(),
			DirectPath:     aud.GetDirectPath(),
			FileSHA256:     aud.GetFileSHA256(),
			FileEncSHA256:  aud.GetFileEncSHA256(),
			DownloadStatus: c.getInitialDownloadStatus(mediaType, fileSize, fromHistory),
		}
	}

	// document message
	if doc := msg.GetDocumentMessage(); doc != nil {
		fileName := doc.GetFileName()
		if fileName == "" {
			fileName = fmt.Sprintf("document_%s", messageID[:8])
			// try to add extension from MIME type
			if ext := mimeToExtension(doc.GetMimetype()); ext != "" {
				fileName += ext
			}
		}
		fileSize := int64(doc.GetFileLength())

		return &storage.MediaMetadata{
			MessageID:      messageID,
			FileName:       fileName,
			FileSize:       fileSize,
			MimeType:       doc.GetMimetype(),
			MediaKey:       doc.GetMediaKey(),
			DirectPath:     doc.GetDirectPath(),
			FileSHA256:     doc.GetFileSHA256(),
			FileEncSHA256:  doc.GetFileEncSHA256(),
			DownloadStatus: c.getInitialDownloadStatus("document", fileSize, fromHistory),
		}
	}

	return nil
}

// shouldAutoDownload checks whether media should be automatically downloaded based on type and size filters.
func (c *Client) shouldAutoDownload(mediaType string, fileSize int64) bool {
	if !c.mediaConfig.AutoDownloadEnabled {
		return false
	}

	// check type filter
	if !c.mediaConfig.AutoDownloadTypes[mediaType] {
		c.log.Debugf("Media type %s not in auto-download types", mediaType)
		return false
	}

	// check size filter (0 = unlimited)
	if c.mediaConfig.AutoDownloadMaxSize > 0 && fileSize > c.mediaConfig.AutoDownloadMaxSize {
		c.log.Debugf("Media size %d bytes exceeds max %d bytes", fileSize, c.mediaConfig.AutoDownloadMaxSize)
		return false
	}

	return true
}

// downloadMedia downloads media from WhatsApp and saves it to disk.
// It returns the relative file path on success.
func (c *Client) downloadMedia(ctx context.Context, msg *waE2E.Message, meta *storage.MediaMetadata, ts time.Time, chatJID string) (string, error) {
	if msg == nil || meta == nil {
		return "", fmt.Errorf("nil message or metadata")
	}

	// get the appropriate downloadable message
	var downloadable any

	if sticker := msg.GetStickerMessage(); sticker != nil {
		downloadable = sticker
	} else if img := msg.GetImageMessage(); img != nil {
		downloadable = img
	} else if vid := msg.GetVideoMessage(); vid != nil {
		downloadable = vid
	} else if aud := msg.GetAudioMessage(); aud != nil {
		downloadable = aud
	} else if doc := msg.GetDocumentMessage(); doc != nil {
		downloadable = doc
	} else {
		return "", fmt.Errorf("unsupported media type")
	}

	// generate unique file path
	filePath, err := c.generateMediaFilePath(meta, ts, chatJID)
	if err != nil {
		return "", fmt.Errorf("failed to generate file path: %w", err)
	}

	// create directory if needed
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// create file
	file, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// download to file using whatsmeow's Download method
	var data []byte
	switch d := downloadable.(type) {
	case *waE2E.ImageMessage:
		data, err = c.wa.Download(ctx, d)
	case *waE2E.VideoMessage:
		data, err = c.wa.Download(ctx, d)
	case *waE2E.AudioMessage:
		data, err = c.wa.Download(ctx, d)
	case *waE2E.DocumentMessage:
		data, err = c.wa.Download(ctx, d)
	case *waE2E.StickerMessage:
		data, err = c.wa.Download(ctx, d)
	default:
		return "", fmt.Errorf("unknown downloadable type")
	}

	if err != nil {
		os.Remove(filePath)
		return "", err
	}

	// write data to file
	if _, err := file.Write(data); err != nil {
		os.Remove(filePath)
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	// verify download
	if err := c.verifyDownload(filePath, meta); err != nil {
		os.Remove(filePath)
		return "", fmt.Errorf("verification failed: %w", err)
	}

	// compute relative file path
	relPath, err := filepath.Rel(c.mediaConfig.StoragePath, filePath)
	if err != nil {
		os.Remove(filePath)
		return "", fmt.Errorf("failed to compute relative media path: %w", err)
	}

	c.log.Infof("Downloaded media %s to %s (%d bytes)", meta.MessageID, relPath, meta.FileSize)

	// return the relative path - caller will update database
	// no in-memory modifications to avoid data races
	return relPath, nil
}

// generateMediaFilePath creates a unique file path based on media metadata.
func chatDirName(jid string) string {
	jid = strings.TrimSpace(jid)
	jid = strings.ReplaceAll(jid, ":", "_")
	jid = strings.ReplaceAll(jid, "/", "_")
	jid = strings.ReplaceAll(jid, "\\", "_")
	jid = strings.ReplaceAll(jid, "@", "_")
	if jid == "" {
		return "_unknown"
	}
	return jid
}

func (c *Client) generateMediaFilePath(meta *storage.MediaMetadata, ts time.Time, chatJID string) (string, error) {
	loc, err := time.LoadLocation(os.Getenv("TIMEZONE"))
	if err != nil || os.Getenv("TIMEZONE") == "" {
		loc = time.FixedZone("HKT", 8*3600)
	}
	if ts.IsZero() {
		ts = time.Now()
	}
	local := ts.In(loc)
	subdir := filepath.Join(chatDirName(chatJID), local.Format("2006"), local.Format("01"), local.Format("02"))

	safeName := sanitizeFilename(meta.FileName)
	if safeName == "" {
		ext := mimeToExtension(meta.MimeType)
		safeName = fmt.Sprintf("media%s", ext)
	}
	id := meta.MessageID
	if len(id) > 24 {
		id = id[:24]
	}
	fileName := fmt.Sprintf("%s-%s", id, safeName)
	return filepath.Join(c.mediaConfig.StoragePath, subdir, fileName), nil
}

// verifyDownload checks file integrity after download.
// Note: whatsmeow's Download() already validates HMAC, encrypted SHA256, and decrypted SHA256.
// This function only performs basic sanity checks on the written file.
func (c *Client) verifyDownload(filePath string, meta *storage.MediaMetadata) error {
	stat, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("file not accessible: %w", err)
	}

	// verify file has non-zero size
	if stat.Size() == 0 {
		return fmt.Errorf("downloaded file is empty")
	}

	// log size comparison for debugging (but don't fail on mismatch)
	// whatsmeow already validated the file, so size differences are just metadata inaccuracies
	if stat.Size() != meta.FileSize {
		c.log.Debugf("File size differs from metadata: wrote %d bytes, metadata claimed %d bytes",
			stat.Size(), meta.FileSize)
	}

	return nil
}

// downloadMediaWithRetry downloads media with retry logic for transient failures.
// It returns the relative file path on success.
func (c *Client) downloadMediaWithRetry(ctx context.Context, msg *waE2E.Message, meta *storage.MediaMetadata, ts time.Time, chatJID string) (string, error) {
	maxRetries := 3
	backoff := time.Second
	var allErrors []string

	for attempt := 1; attempt <= maxRetries; attempt++ {
		filePath, err := c.downloadMedia(ctx, msg, meta, ts, chatJID)
		if err == nil {
			return filePath, nil
		}

		allErrors = append(allErrors, fmt.Sprintf("attempt %d: %v", attempt, err))

		// is error retryable?
		// 404/410 errors indicate expired/deleted media - don't retry
		if errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404) ||
			errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410) {
			return "", err
		}

		c.log.Warnf("Download attempt %d/%d failed: %v", attempt, maxRetries, err)

		if attempt < maxRetries {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
				backoff *= 2 // backoff
			}
		}
	}

	// combine all errors into a single message
	return "", fmt.Errorf("download failed after %d attempts: %s", maxRetries, strings.Join(allErrors, "; "))
}

// intPtr returns a pointer to the given integer value.
func intPtr(i int) *int {
	return &i
}

// sanitizeFilename removes unsafe characters from a filename and truncates it to a safe length.
func sanitizeFilename(name string) string {
	if name == "" {
		return ""
	}

	// remove path separators and other unsafe characters
	var result []rune
	for _, r := range name {
		if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
			result = append(result, '_')
		} else if unicode.IsPrint(r) {
			result = append(result, r)
		}
	}

	filename := string(result)

	// truncate filename to 200 runes (Unicode-safe)
	const maxFilenameLen = 200
	runes := []rune(filename)
	if len(runes) > maxFilenameLen {
		ext := filepath.Ext(filename)
		extRunes := []rune(ext)

		// if extension is too long, just truncate the whole filename
		if len(extRunes) >= maxFilenameLen {
			filename = string(runes[:maxFilenameLen])
		} else {
			baseLen := maxFilenameLen - len(extRunes)
			filename = string(runes[:baseLen]) + ext
		}
	}

	return filename
}

// mimeToExtension converts a MIME type to a file extension.
func mimeToExtension(mime string) string {
	extensions := map[string]string{
		"image/jpeg":      ".jpg",
		"image/jpg":       ".jpg",
		"image/png":       ".png",
		"image/gif":       ".gif",
		"image/webp":      ".webp",
		"video/mp4":       ".mp4",
		"video/3gpp":      ".3gp",
		"video/quicktime": ".mov",
		"audio/ogg":       ".ogg",
		"audio/mpeg":      ".mp3",
		"audio/mp4":       ".m4a",
		"audio/aac":       ".aac",
		"application/pdf": ".pdf",
		"application/zip": ".zip",
		"text/plain":      ".txt",
	}

	if ext, ok := extensions[mime]; ok {
		return ext
	}

	// try to extract extension from MIME type
	parts := strings.Split(mime, "/")
	if len(parts) == 2 {
		return "." + parts[1]
	}

	return ""
}

// hashFile computes the SHA256 hash of a file.
func hashFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return nil, err
	}

	return hasher.Sum(nil), nil
}
