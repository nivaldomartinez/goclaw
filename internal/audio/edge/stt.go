package edge

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/audio"
)

// STTConfig configures the Edge STT provider (free, no API key).
type STTConfig struct {
	// Model is the Whisper model size: tiny, base, small, medium, large.
	// Larger models are more accurate but slower. Default: "base".
	Model     string
	Language  string // BCP-47 hint, empty = auto-detect
	TimeoutMs int    // default 60000
}

// STTProvider implements audio.STTProvider via the faster-whisper / whisper CLI.
// Requires: pip install faster-whisper  (or: pip install openai-whisper)
// No API key needed — runs fully local.
type STTProvider struct {
	model     string
	language  string
	timeoutMs int
}

// NewSTTProvider returns an Edge STT provider with defaults applied.
func NewSTTProvider(cfg STTConfig) *STTProvider {
	p := &STTProvider{
		model:     cfg.Model,
		language:  cfg.Language,
		timeoutMs: cfg.TimeoutMs,
	}
	if p.model == "" {
		p.model = "base"
	}
	if p.timeoutMs <= 0 {
		p.timeoutMs = 60000
	}
	return p
}

// Name returns the stable provider identifier.
func (p *STTProvider) Name() string { return "edge" }

// Transcribe converts audio to text via the local faster-whisper / whisper CLI.
// FilePath is preferred over Bytes. Language from opts overrides config-level hint.
func (p *STTProvider) Transcribe(ctx context.Context, in audio.STTInput, opts audio.STTOptions) (*audio.TranscriptResult, error) {
	filePath, cleanup, err := sttResolveFilePath(in)
	if err != nil {
		return nil, fmt.Errorf("edge stt: resolve input: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	lang := p.language
	if opts.Language != "" {
		lang = opts.Language
	}

	outDir := os.TempDir()
	args := []string{
		filePath,
		"--model", p.model,
		"--output_format", "txt",
		"--output_dir", outDir,
	}
	if lang != "" {
		args = append(args, "--language", lang)
	}

	timeout := time.Duration(p.timeoutMs) * time.Millisecond
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cli := resolveCLI()
	cmd := exec.CommandContext(cmdCtx, cli, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("edge stt (%s): %w — %s", cli, err, strings.TrimSpace(string(out)))
	}

	// Output file: <basename_without_ext>.txt in outDir.
	base := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	txtPath := filepath.Join(outDir, base+".txt")
	defer os.Remove(txtPath)

	raw, err := os.ReadFile(txtPath)
	if err != nil {
		return nil, fmt.Errorf("edge stt: read output: %w", err)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, fmt.Errorf("edge stt: empty transcription")
	}
	return &audio.TranscriptResult{Text: text, Provider: "edge"}, nil
}

// resolveCLI returns the first whisper-compatible CLI found on PATH.
// faster-whisper is preferred (faster); openai-whisper is the fallback.
func resolveCLI() string {
	if _, err := exec.LookPath("faster-whisper"); err == nil {
		return "faster-whisper"
	}
	return "whisper"
}

// sttResolveFilePath returns a usable file path. When only Bytes is set,
// writes a 0600 temp file and returns a cleanup func to remove it.
func sttResolveFilePath(in audio.STTInput) (path string, cleanup func(), err error) {
	if in.FilePath != "" {
		return in.FilePath, nil, nil
	}
	if len(in.Bytes) == 0 {
		return "", nil, fmt.Errorf("neither FilePath nor Bytes provided")
	}
	ext := sttExtFromMime(in.MimeType)
	f, err := os.CreateTemp("", "stt-edge-*"+ext)
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}
	if err := os.Chmod(f.Name(), 0600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := f.Write(in.Bytes); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("write temp file: %w", err)
	}
	f.Close()
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

// sttExtFromMime returns a file extension for a MIME type.
func sttExtFromMime(mime string) string {
	m := strings.ToLower(strings.SplitN(mime, ";", 2)[0])
	switch m {
	case "audio/ogg", "audio/opus":
		return ".ogg"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/wav", "audio/wave":
		return ".wav"
	case "audio/mp4", "audio/m4a":
		return ".m4a"
	case "audio/webm":
		return ".webm"
	case "audio/flac":
		return ".flac"
	default:
		return ".ogg"
	}
}
