package audio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// ErrAllSTTProvidersFailed is returned when every provider in the chain fails.
var ErrAllSTTProvidersFailed = errors.New("all STT providers failed")

// defaultSTTChain is the built-in fallback order when no explicit chain is set.
var defaultSTTChain = []string{"elevenlabs", "proxy"}

// Transcribe tries providers in chain order. Returns first success.
// Wraps last error with ErrAllSTTProvidersFailed on total failure.
func (m *Manager) Transcribe(ctx context.Context, in STTInput, opts STTOptions) (*TranscriptResult, error) {
	chain := m.resolveSTTChain(ctx)
	if len(chain) == 0 {
		return nil, fmt.Errorf("%w: chain is empty", ErrAllSTTProvidersFailed)
	}

	var lastErr error
	for _, name := range chain {
		p, ok := m.sttProviders[name]
		if !ok {
			slog.Warn("audio.stt provider not registered, skipping", "provider", name)
			continue
		}
		res, err := p.Transcribe(ctx, in, opts)
		if err == nil {
			return res, nil
		}
		slog.Warn("audio.stt provider failed", "provider", name, "error", err)
		lastErr = err
	}

	if lastErr != nil {
		return nil, fmt.Errorf("%w: %w", ErrAllSTTProvidersFailed, lastErr)
	}
	return nil, fmt.Errorf("%w: no providers matched in chain %v", ErrAllSTTProvidersFailed, chain)
}

// SetSTTChain sets an explicit provider order for STT dispatch.
// Call before first use; not thread-safe after concurrent Transcribe calls.
func (m *Manager) SetSTTChain(chain []string) {
	m.sttChain = chain
}

// RegisterChannelSTT registers STT providers scoped to a specific channel name
// (e.g. "telegram"). Channel-scoped providers take precedence over the manager
// default chain when resolveSTTChain detects a matching channel in ctx.
func (m *Manager) RegisterChannelSTT(channelName string, providers ...STTProvider) {
	if m.channelSTTOverrides == nil {
		m.channelSTTOverrides = make(map[string][]string)
	}
	names := make([]string, 0, len(providers))
	for _, p := range providers {
		m.sttProviders[p.Name()+":"+channelName] = p
		names = append(names, p.Name()+":"+channelName)
	}
	m.channelSTTOverrides[channelName] = names
}

// resolveSTTChain returns the ordered provider names for the current call.
// Precedence: channel override providers first, then global chain as fallback.
// This ensures that if per-channel providers (e.g. legacy proxy) fail, the
// global chain (e.g. ElevenLabs) is still tried rather than giving up.
func (m *Manager) resolveSTTChain(ctx context.Context) []string {
	seen := make(map[string]bool)
	var out []string

	addUnique := func(names []string) {
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}

	// (1) Channel override: channel-scoped providers first.
	if ch := channelFromCtx(ctx); ch != "" {
		if overrides, ok := m.channelSTTOverrides[ch]; ok && len(overrides) > 0 {
			addUnique(overrides)
		}
	}

	// (2) Append explicit chain as fallback (or primary when no channel override).
	if len(m.sttChain) > 0 {
		addUnique(m.sttChain)
		return out
	}

	// (3) Append default chain filtered to registered providers.
	for _, name := range defaultSTTChain {
		if _, ok := m.sttProviders[name]; ok {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}
