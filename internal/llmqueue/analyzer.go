package llmqueue

import (
	"context"
	"fmt"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
)

// QueuedAnalyzer wraps an analysis.Analyzer with the LLM job queue.
// It ensures that all analysis calls go through the queue and are serialized.
type QueuedAnalyzer struct {
	inner analysis.Analyzer
	queue *Queue
}

// NewQueuedAnalyzer wraps an analyzer with queue-based serialization.
func NewQueuedAnalyzer(inner analysis.Analyzer, queue *Queue) *QueuedAnalyzer {
	return &QueuedAnalyzer{inner: inner, queue: queue}
}

// Analyze submits the analysis to the queue and blocks until completion.
// This ensures only MaxConcurrency analyses run at once, protecting Ollama.
func (qa *QueuedAnalyzer) Analyze(ctx context.Context, req analysis.AnalyzeRequest) (*analysis.AnalysisReport, error) {
	resultCh := make(chan analyzeResult, 1)

	ok := qa.queue.Submit(Job{
		ID:       fmt.Sprintf("analyze:%s", req.Session.ID),
		Priority: 0,
		Fn: func(jobCtx context.Context) error {
			// Use the job context merged with the caller's context.
			mergedCtx := ctx
			if jobCtx.Err() != nil {
				mergedCtx = jobCtx
			}
			report, err := qa.inner.Analyze(mergedCtx, req)
			resultCh <- analyzeResult{report: report, err: err}
			return err
		},
	})

	if !ok {
		return nil, fmt.Errorf("LLM queue full — try again later")
	}

	// Wait for result.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-resultCh:
		return res.report, res.err
	}
}

// Name delegates to the inner analyzer.
func (qa *QueuedAnalyzer) Name() analysis.AdapterName {
	return qa.inner.Name() + "-queued"
}

type analyzeResult struct {
	report *analysis.AnalysisReport
	err    error
}
