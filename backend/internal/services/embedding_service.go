package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

// EmbeddingService computes vector embeddings for short texts using whichever
// provider supports `/openai/v1/embeddings` or the Bedrock native invoke
// endpoint for an embedding model. Used by the memory layer for vector
// retrieval (the LLM-based selector we ship alongside it gets too expensive
// past a few dozen memories per user).
//
// Provider selection (priority order, first that works wins):
//
//  1. EMBEDDING_PROVIDER_URL + EMBEDDING_PROVIDER_KEY env vars — explicit
//     OpenAI-compatible /v1/embeddings endpoint. Use this if you have a
//     dedicated embedding key (OpenAI, Together, Voyage, etc.).
//
//  2. Bedrock native invoke at bedrock-runtime.<region>.amazonaws.com/model/
//     amazon.titan-embed-text-v2:0/invoke, authenticated with the Bedrock
//     provider row's bearer API key (the ABSK long-term key). Picked up
//     from the providers table — looks for a row with name='Bedrock' or
//     whose base_url contains "bedrock-runtime".
//
// Output is always 1024-d for Titan v2 / 1536-d for OpenAI small / 1024-d
// for Cohere v3. We don't normalise dimension — callers compare embeddings
// pairwise so as long as the same provider was used for both, cosine works.
//
// Failures degrade gracefully: callers treat (nil, err) as "no embedding
// available" and fall back to LLM-based memory selection.
type EmbeddingService struct {
	providerService *ProviderService
	httpClient      *http.Client

	// Static config — never changes after init.
	explicitURL string
	explicitKey string

	// Cached Bedrock provider creds (the bearer key + region URL). Resolved
	// lazily so the service can boot before any provider exists.
	bedrockMu      sync.RWMutex
	bedrockBaseURL string // e.g. https://bedrock-runtime.ap-south-1.amazonaws.com
	bedrockKey     string
	bedrockChecked bool
}

// NewEmbeddingService wires the service. providerService may be nil when
// running in isolation (tests); in that case only EMBEDDING_PROVIDER_URL
// path is usable.
func NewEmbeddingService(providerService *ProviderService, explicitURL, explicitKey string) *EmbeddingService {
	return &EmbeddingService{
		providerService: providerService,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		explicitURL: strings.TrimRight(explicitURL, "/"),
		explicitKey: explicitKey,
	}
}

// Embed turns a single piece of text into a vector. Returns (nil, err) when
// no provider is configured or the call fails — callers should degrade.
//
// Texts are clipped at 8000 chars before embedding to keep us well inside
// any provider's token limit; longer inputs are usually message-history
// concatenations that don't benefit from full inclusion.
func (s *EmbeddingService) Embed(ctx context.Context, text string) ([]float32, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty text")
	}
	if len(text) > 8000 {
		text = text[:8000]
	}

	if s.explicitURL != "" && s.explicitKey != "" {
		vec, err := s.embedOpenAICompatible(ctx, s.explicitURL, s.explicitKey, text)
		if err == nil {
			return vec, nil
		}
		log.Printf("⚠️ [EMBED] explicit provider failed: %v — trying Bedrock fallback", err)
	}

	bedrockURL, bedrockKey := s.resolveBedrock(ctx)
	if bedrockURL != "" && bedrockKey != "" {
		return s.embedTitanV2(ctx, bedrockURL, bedrockKey, text)
	}

	return nil, fmt.Errorf("no embedding provider configured")
}

// EmbedBatch embeds multiple texts in one go where the provider supports
// batching (Titan v2 does not — it's single-input — so we just loop).
// Optimised in the future if we add OpenAI/Cohere batched paths.
func (s *EmbeddingService) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		vec, err := s.Embed(ctx, t)
		if err != nil {
			return nil, fmt.Errorf("embed[%d]: %w", i, err)
		}
		out[i] = vec
	}
	return out, nil
}

// Available reports whether this service can produce embeddings right now.
// Used by the memory selection service to decide whether to vector-search
// vs fall back to the old LLM-only selector.
func (s *EmbeddingService) Available(ctx context.Context) bool {
	if s.explicitURL != "" && s.explicitKey != "" {
		return true
	}
	url, key := s.resolveBedrock(ctx)
	return url != "" && key != ""
}

// resolveBedrock pulls the bearer-key + region base URL from the providers
// table. Cached after the first successful lookup.
func (s *EmbeddingService) resolveBedrock(ctx context.Context) (string, string) {
	s.bedrockMu.RLock()
	if s.bedrockChecked {
		bu, bk := s.bedrockBaseURL, s.bedrockKey
		s.bedrockMu.RUnlock()
		return bu, bk
	}
	s.bedrockMu.RUnlock()

	s.bedrockMu.Lock()
	defer s.bedrockMu.Unlock()
	if s.bedrockChecked {
		return s.bedrockBaseURL, s.bedrockKey
	}
	s.bedrockChecked = true

	if s.providerService == nil {
		return "", ""
	}
	providers, err := s.providerService.GetAll()
	if err != nil {
		return "", ""
	}
	for _, p := range providers {
		if !p.Enabled {
			continue
		}
		lower := strings.ToLower(p.BaseURL)
		if !strings.Contains(lower, "bedrock-runtime") {
			continue
		}
		// Strip the /openai/v1 suffix to get the native-invoke root.
		root := strings.TrimRight(p.BaseURL, "/")
		root = strings.TrimSuffix(root, "/openai/v1")
		s.bedrockBaseURL = root
		s.bedrockKey = p.APIKey
		log.Printf("📍 [EMBED] Bedrock embedding endpoint resolved: %s", root)
		return root, p.APIKey
	}
	return "", ""
}

// embedTitanV2 calls Bedrock native invoke for amazon.titan-embed-text-v2:0.
// Returns the embedding vector (1024-d).
func (s *EmbeddingService) embedTitanV2(ctx context.Context, baseURL, apiKey, text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"inputText": text,
	})
	url := baseURL + "/model/amazon.titan-embed-text-v2:0/invoke"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("titan invoke HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("titan decode: %w", err)
	}
	if len(parsed.Embedding) == 0 {
		return nil, fmt.Errorf("titan returned empty embedding")
	}
	return parsed.Embedding, nil
}

// embedOpenAICompatible hits /v1/embeddings on whatever URL+key the operator
// provided via env. We don't pin a model — assumed configured upstream — and
// fall back to text-embedding-3-small only when the caller didn't pass any
// hint. (We don't expose that hint yet; left as a hook for future tuning.)
func (s *EmbeddingService) embedOpenAICompatible(ctx context.Context, baseURL, apiKey, text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"model": "text-embedding-3-small",
		"input": text,
	})
	url := baseURL + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("openai-compat HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("openai-compat decode: %w", err)
	}
	if len(parsed.Data) == 0 || len(parsed.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("openai-compat empty embedding")
	}
	return parsed.Data[0].Embedding, nil
}

// CosineSimilarity returns the cosine similarity between two equal-length
// vectors. Returns 0 on mismatched / empty inputs. Exported because the
// memory storage service uses it to rank candidates.
//
// Uses math.Sqrt for full IEEE 754 precision. An earlier version of this
// function used a hand-rolled 4-iteration Newton's method to "avoid the
// math import" — for vectors of even modest magnitude that converges to
// ~2-3 significant digits, which silently skewed every retrieval ranking
// (identical vectors scored ~0.989 instead of 1.0; parallel-but-scaled
// vectors scored ~0.97). Caught by memory_vector_test.go.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
