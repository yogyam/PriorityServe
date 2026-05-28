package worker

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/yourusername/priorityserve/internal/scheduler"
)

// Backend is an HTTP client pointed at the llama.cpp server.
type Backend struct {
	client  *http.Client
	baseURL string
}

func NewBackend(baseURL string) *Backend {
	return &Backend{
		client: &http.Client{
			// No top-level Timeout — it would kill streaming responses mid-stream.
			// Context cancellation on each request handles timeouts instead.
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:    10,
				IdleConnTimeout: 90 * time.Second,
			},
		},
		baseURL: baseURL,
	}
}

// HealthCheck pings the backend and returns an error if it is not reachable.
func (b *Backend) HealthCheck() error {
	resp, err := b.client.Get(b.baseURL + "/health")
	if err != nil {
		return fmt.Errorf("backend unreachable at %s: %w", b.baseURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("backend returned %d", resp.StatusCode)
	}
	return nil
}

// Do executes an InferenceRequest against llama.cpp and returns a buffered Result.
// Phase 2: buffers the full response. Phase 3 will add streaming.
func (b *Backend) Do(req *scheduler.InferenceRequest) scheduler.Result {
	httpReq, err := http.NewRequestWithContext(
		req.Ctx,
		http.MethodPost,
		b.baseURL+"/v1/chat/completions",
		bytes.NewReader(req.Body),
	)
	if err != nil {
		return scheduler.Result{StatusCode: 500, Err: fmt.Errorf("building request: %w", err)}
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return scheduler.Result{StatusCode: 502, Err: fmt.Errorf("backend request: %w", err)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return scheduler.Result{StatusCode: 502, Err: fmt.Errorf("reading response: %w", err)}
	}

	return scheduler.Result{
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       body,
	}
}

// Forward proxies r to the llama.cpp backend, streaming the response into w.
// It copies headers and status code unchanged.
func (b *Backend) Forward(w http.ResponseWriter, r *http.Request) error {
	url := b.baseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, url, r.Body)
	if err != nil {
		return fmt.Errorf("building backend request: %w", err)
	}
	req.Header = r.Header.Clone()
	// Don't forward the hop-by-hop headers that the client sent to us.
	req.Header.Del("X-Priority")

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("backend request failed: %w", err)
	}
	defer resp.Body.Close()

	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)

	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return nil // client disconnected
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("reading backend response: %w", readErr)
		}
	}
	return nil
}
