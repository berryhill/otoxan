package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/silas/otoxan/internal/mcp"
	"github.com/silas/otoxan/internal/store/memory"
	"github.com/silas/otoxan/internal/store/plans"
	"github.com/silas/otoxan/internal/store/reports"
)

// SearchArgs is the input schema for search.
type SearchArgs struct {
	Query string `json:"query" jsonschema:"required,description=Search query text"`
	K     int    `json:"k" jsonschema:"description=Number of results to return (default 10)"`
}

func registerTools(srv *mcp.Server, memStore *memory.MemoryStore, planStore *plans.PlanStore, reportStore *reports.ReportStore) {
	srv.Register(mcp.Tool{
		Name:        "search",
		Description: "Search across memories, plans, and reports using a query string. Returns merged results ranked by relevance.",
		InputSchema: mcp.SchemaOf[SearchArgs](),
		Handler:     handleSearch(memStore, planStore, reportStore),
	})
}

// searchHit is a unified result from any store.
type searchHit struct {
	Source    string  `json:"source"`
	ID        string  `json:"id"`
	Title     string  `json:"title,omitempty"`
	Content   string  `json:"content,omitempty"`
	Score     float64 `json:"score"`
	CreatedAt string  `json:"created_at,omitempty"`
}

func handleSearch(memStore *memory.MemoryStore, planStore *plans.PlanStore, reportStore *reports.ReportStore) func(ctx context.Context, raw json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args SearchArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.Query == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "query is required"}
		}
		if args.K <= 0 {
			args.K = 10
		}

		var wg sync.WaitGroup
		var memHits, planHits, reportHits []searchHit
		var memErr, planErr, reportErr error

		wg.Add(3)
		go func() {
			defer wg.Done()
			memHits, memErr = searchMemories(ctx, memStore, args.Query, args.K)
		}()
		go func() {
			defer wg.Done()
			planHits, planErr = searchPlans(ctx, planStore, args.Query, args.K)
		}()
		go func() {
			defer wg.Done()
			reportHits, reportErr = searchReports(ctx, reportStore, args.Query, args.K)
		}()
		wg.Wait()

		if memErr != nil && planErr != nil && reportErr != nil {
			return nil, fmt.Errorf("all searches failed: memory=%v plans=%v reports=%v", memErr, planErr, reportErr)
		}

		all := append(append(memHits, planHits...), reportHits...)
		sort.Slice(all, func(i, j int) bool {
			return all[i].Score > all[j].Score
		})
		if len(all) > args.K {
			all = all[:args.K]
		}
		return all, nil
	}
}

func searchMemories(ctx context.Context, store *memory.MemoryStore, query string, k int) ([]searchHit, error) {
	mems, err := store.List(ctx, memory.ListOptions{Limit: 1000})
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	var hits []searchHit
	for _, m := range mems {
		score := textScore(m.Content, query)
		if score > 0 || containsString(m.Tags, query) {
			if score == 0 {
				score = 0.1
			}
			hits = append(hits, searchHit{
				Source:    "memory",
				ID:        m.MemoryID,
				Content:   m.Content,
				Score:     score,
				CreatedAt: m.CreatedAt.Format("2006-01-02T15:04:05Z"),
			})
			if len(hits) >= k {
				break
			}
		}
	}
	return hits, nil
}

func searchPlans(ctx context.Context, store *plans.PlanStore, query string, k int) ([]searchHit, error) {
	pls, err := store.List(ctx, plans.ListOptions{Limit: 1000})
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	var hits []searchHit
	for _, p := range pls {
		score := textScore(p.Title+" "+p.Content, query)
		if score > 0 || containsString(p.Tags, query) {
			if score == 0 {
				score = 0.1
			}
			hits = append(hits, searchHit{
				Source:    "plan",
				ID:        p.PlanID,
				Title:     p.Title,
				Content:   p.Content,
				Score:     score,
				CreatedAt: p.CreatedAt.Format("2006-01-02T15:04:05Z"),
			})
			if len(hits) >= k {
				break
			}
		}
	}
	return hits, nil
}

func searchReports(ctx context.Context, store *reports.ReportStore, query string, k int) ([]searchHit, error) {
	rpts, err := store.List(ctx, reports.ListOptions{Limit: 1000})
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}
	var hits []searchHit
	for _, r := range rpts {
		score := textScore(r.Title+" "+r.Content, query)
		if score > 0 || containsString(r.Tags, query) {
			if score == 0 {
				score = 0.1
			}
			hits = append(hits, searchHit{
				Source:    "report",
				ID:        r.ReportID,
				Title:     r.Title,
				Content:   r.Content,
				Score:     score,
				CreatedAt: r.CreatedAt.Format("2006-01-02T15:04:05Z"),
			})
			if len(hits) >= k {
				break
			}
		}
	}
	return hits, nil
}

// textScore returns a simple relevance score based on word overlap.
func textScore(text, query string) float64 {
	text = strings.ToLower(text)
	query = strings.ToLower(query)
	words := strings.Fields(query)
	if len(words) == 0 {
		return 0
	}
	matches := 0
	for _, w := range words {
		if strings.Contains(text, w) {
			matches++
		}
	}
	return float64(matches) / float64(len(words))
}

func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if strings.Contains(strings.ToLower(v), strings.ToLower(s)) {
			return true
		}
	}
	return false
}
