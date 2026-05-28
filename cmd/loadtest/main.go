package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ---- request/response types ------------------------------------------------

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ---- per-request result ----------------------------------------------------

type result struct {
	priority  string
	latencyMs float64
	status    int
	err       error
}

// ---- JSON output format ----------------------------------------------------

type tierStats struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50_ms"`
	P95   float64 `json:"p95_ms"`
	P99   float64 `json:"p99_ms"`
	Max   float64 `json:"max_ms"`
}

type runOutput struct {
	Timestamp   string               `json:"timestamp"`
	Addr        string               `json:"addr"`
	TotalReqs   int                  `json:"total_requests"`
	Concurrency int                  `json:"concurrency"`
	HighPct     int                  `json:"high_pct"`
	MedPct      int                  `json:"med_pct"`
	LowPct      int                  `json:"low_pct"`
	Prompt      string               `json:"prompt"`
	DurationSec float64              `json:"duration_sec"`
	ReqPerSec   float64              `json:"req_per_sec"`
	Errors      int                  `json:"errors"`
	Tiers       map[string]tierStats `json:"tiers"`
}

// ---- main ------------------------------------------------------------------

func main() {
	addr := flag.String("addr", "http://localhost:8080", "PriorityServe address")
	n := flag.Int("n", 60, "total number of requests")
	c := flag.Int("c", 20, "max concurrent requests in flight")
	highPct := flag.Int("high", 10, "% of high priority requests")
	medPct := flag.Int("med", 20, "% of medium priority requests")
	prompt := flag.String("prompt", "Count from 1 to 15, one number per line.", "prompt sent with every request")
	save := flag.String("save", "", "write JSON results to this file (default: auto-named in results/)")
	flag.Parse()

	lowPct := 100 - *highPct - *medPct
	if lowPct < 0 {
		fmt.Fprintln(os.Stderr, "error: high + med percentages exceed 100")
		os.Exit(1)
	}

	fmt.Printf("PriorityServe Load Test\n")
	fmt.Printf("  addr=%s  n=%d  c=%d  high=%d%%  med=%d%%  low=%d%%\n\n",
		*addr, *n, *c, *highPct, *medPct, lowPct)

	// Build a shuffled priority list matching the requested distribution.
	priorities := buildPriorityList(*n, *highPct, *medPct)

	results := make([]result, *n)
	sem := make(chan struct{}, *c)
	var wg sync.WaitGroup

	bodyBytes, _ := json.Marshal(chatRequest{
		Model:    "llama3.2",
		Messages: []message{{Role: "user", Content: *prompt}},
	})

	client := &http.Client{}
	var done int64
	start := time.Now()

	for i, p := range priorities {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, priority string) {
			defer wg.Done()
			defer func() { <-sem }()

			t0 := time.Now()
			req, _ := http.NewRequest(http.MethodPost,
				*addr+"/v1/chat/completions",
				bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Priority", priority)

			resp, err := client.Do(req)
			latencyMs := float64(time.Since(t0).Milliseconds())

			if err != nil {
				results[idx] = result{priority: priority, err: err}
				printProgress(&done, *n)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			results[idx] = result{
				priority:  priority,
				latencyMs: latencyMs,
				status:    resp.StatusCode,
			}
			printProgress(&done, *n)
		}(i, p)
	}

	wg.Wait()
	elapsed := time.Since(start)
	fmt.Println()

	// Aggregate per tier.
	byTier := map[string][]float64{"high": {}, "medium": {}, "low": {}}
	errors := 0
	for _, r := range results {
		if r.err != nil || r.status >= 400 {
			errors++
			continue
		}
		byTier[r.priority] = append(byTier[r.priority], r.latencyMs)
	}

	// Print table.
	fmt.Printf("\n%-10s  %6s  %8s  %8s  %8s  %8s\n",
		"Priority", "Count", "p50 (ms)", "p95 (ms)", "p99 (ms)", "Max (ms)")
	fmt.Println("--------------------------------------------------------------")

	tiers := map[string]tierStats{}
	for _, tier := range []string{"high", "medium", "low"} {
		lats := byTier[tier]
		if len(lats) == 0 {
			fmt.Printf("%-10s  %6d  %8s  %8s  %8s  %8s\n", tier, 0, "—", "—", "—", "—")
			tiers[tier] = tierStats{}
			continue
		}
		sort.Float64s(lats)
		s := tierStats{
			Count: len(lats),
			P50:   pct(lats, 50),
			P95:   pct(lats, 95),
			P99:   pct(lats, 99),
			Max:   lats[len(lats)-1],
		}
		tiers[tier] = s
		fmt.Printf("%-10s  %6d  %8.0f  %8.0f  %8.0f  %8.0f\n",
			tier, s.Count, s.P50, s.P95, s.P99, s.Max)
	}

	fmt.Printf("\nTotal: %d requests | Concurrency: %d | Duration: %.1fs | Throughput: %.1f req/s | Errors: %d\n",
		*n, *c, elapsed.Seconds(), float64(*n-errors)/elapsed.Seconds(), errors)

	// Separation ratio: the key result.
	h := tiers["high"]
	l := tiers["low"]
	if h.Count > 0 && l.Count > 0 && l.P95 > 0 {
		fmt.Printf("\nLatency separation (high p95 / low p95): %.2fx\n", l.P95/h.P95)
	}

	// Save JSON.
	out := runOutput{
		Timestamp:   time.Now().Format(time.RFC3339),
		Addr:        *addr,
		TotalReqs:   *n,
		Concurrency: *c,
		HighPct:     *highPct,
		MedPct:      *medPct,
		LowPct:      lowPct,
		Prompt:      *prompt,
		DurationSec: elapsed.Seconds(),
		ReqPerSec:   float64(*n-errors) / elapsed.Seconds(),
		Errors:      errors,
		Tiers:       tiers,
	}

	outPath := *save
	if outPath == "" {
		outPath = fmt.Sprintf("results/loadtest_%s_n%d_c%d.json",
			time.Now().Format("20060102_150405"), *n, *c)
	}
	if data, err := json.MarshalIndent(out, "", "  "); err == nil {
		if err := os.WriteFile(outPath, data, 0644); err == nil {
			fmt.Printf("Results saved to %s\n", outPath)
		}
	}
}

// ---- helpers ---------------------------------------------------------------

func buildPriorityList(n, highPct, medPct int) []string {
	priorities := make([]string, 0, n)
	for i := range n {
		p := i * 100 / n
		switch {
		case p < highPct:
			priorities = append(priorities, "high")
		case p < highPct+medPct:
			priorities = append(priorities, "medium")
		default:
			priorities = append(priorities, "low")
		}
	}
	rand.Shuffle(len(priorities), func(i, j int) {
		priorities[i], priorities[j] = priorities[j], priorities[i]
	})
	return priorities
}

func pct(sorted []float64, p float64) float64 {
	idx := int(float64(len(sorted)-1) * p / 100.0)
	return sorted[idx]
}

var progressMu sync.Mutex

func printProgress(done *int64, total int) {
	d := atomic.AddInt64(done, 1)
	progressMu.Lock()
	fmt.Printf("\r  progress: %d/%d", d, total)
	progressMu.Unlock()
}
