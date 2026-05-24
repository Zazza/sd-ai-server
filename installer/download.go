package installer

import (
	"fmt"
	"io"
	"log"
	"time"

	"sd-studio-server/process"
)

type progressWriter struct {
	w        io.Writer
	total    int64
	written  int64
	key      string
	target   string
	lastLog  int64
	lastTime int64
	inst     *Installer
	lb       *process.RingBuffer
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	if err != nil {
		return n, err
	}
	pw.written += int64(n)
	now := time.Now().UnixMilli()
	shouldLog := pw.written-pw.lastLog >= 10*1024*1024 || now-pw.lastTime >= 5000
	if shouldLog {
		pw.lastLog = pw.written
		pw.lastTime = now
		pct := ""
		if pw.total > 0 {
			pct = fmt.Sprintf(" (%.0f%%)", float64(pw.written)/float64(pw.total)*100)
		}
		msg := fmt.Sprintf("Downloading %s%s", FormatBytes(pw.written), pct)
		pw.inst.setProgress(pw.key, msg)
		pw.lb.Write(msg)
		log.Printf("[%s] %s", pw.key, msg)
	}
	return n, nil
}

func FormatBytes(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
