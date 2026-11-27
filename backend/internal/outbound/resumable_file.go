package outbound

import (
	"io"
	"net/http"
	"os"
)

func completedResumableFile(filePath, contentType, sourcePath string, size int64) (ResumableResult, bool, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return ResumableResult{}, true, err
	}
	defer file.Close()
	header := make([]byte, 512)
	n, err := io.ReadFull(file, header)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return ResumableResult{}, true, err
	}
	mimeType := normalizeResumableMimeType(contentType)
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = http.DetectContentType(header[:n])
	}
	return ResumableResult{FilePath: filePath, Size: size, MimeType: mimeType, FileName: resumableFileName(sourcePath)}, false, nil
}
