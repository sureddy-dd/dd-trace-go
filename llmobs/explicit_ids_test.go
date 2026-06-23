// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/llmobs"
)

// TestStartSpanExplicitIDs verifies that WithSpanID/WithTraceID/WithParentID
// drive the emitted span event's span_id, trace_id and parent_id, supporting
// offline/reconstruction use cases that emit spans with deterministic IDs.
func TestStartSpanExplicitIDs(t *testing.T) {
	ctx := context.Background()

	const (
		explicitSpanID  uint64 = 1234567890123456789
		explicitTraceID        = "0123456789abcdef0123456789abcdef"
		explicitParent         = "9876543210987654321"
	)

	t.Run("all-explicit-ids-honored", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartLLMSpan(ctx, "reconstructed-llm",
			llmobs.WithSpanID(explicitSpanID),
			llmobs.WithTraceID(explicitTraceID),
			llmobs.WithParentID(explicitParent),
		)
		span.Finish()

		wantSpanID := strconv.FormatUint(explicitSpanID, 10)
		assert.Equal(t, wantSpanID, span.SpanID())
		assert.Equal(t, explicitTraceID, span.TraceID())
		// WithSpanID seeds the root span's APM trace ID lower 64 bits with the
		// span ID; the LLMObs trace ID is driven independently by WithTraceID.
		apmTraceID := span.APMTraceID()
		require.Len(t, apmTraceID, 32)
		assert.Equal(t, fmt.Sprintf("%016x", explicitSpanID), apmTraceID[16:])

		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, wantSpanID, spans[0].SpanID)
		assert.Equal(t, explicitTraceID, spans[0].TraceID)
		assert.Equal(t, explicitParent, spans[0].ParentID)
	})

	t.Run("explicit-span-id-sets-apm-trace-id", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartLLMSpan(ctx, "span-id-only", llmobs.WithSpanID(explicitSpanID))
		span.Finish()

		wantSpanID := strconv.FormatUint(explicitSpanID, 10)
		assert.Equal(t, wantSpanID, span.SpanID())
		// The APM trace ID lower 64 bits follow the span ID, but the LLMObs trace
		// ID stays independently generated rather than derived from it.
		apmTraceID := span.APMTraceID()
		require.Len(t, apmTraceID, 32)
		assert.Equal(t, fmt.Sprintf("%016x", explicitSpanID), apmTraceID[16:])
		assert.Len(t, span.TraceID(), 32)
		assert.NotEqual(t, apmTraceID, span.TraceID())

		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, wantSpanID, spans[0].SpanID)
		assert.Equal(t, "undefined", spans[0].ParentID)
	})

	t.Run("explicit-trace-id-only", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartLLMSpan(ctx, "trace-id-only", llmobs.WithTraceID(explicitTraceID))
		span.Finish()

		assert.Equal(t, explicitTraceID, span.TraceID())
		// The span ID is still generated (not the explicit trace ID), and the
		// backing apm_trace_id is independent of the chosen LLMObs trace ID.
		assert.NotEmpty(t, span.SpanID())
		assert.NotEqual(t, explicitTraceID, span.APMTraceID())

		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, explicitTraceID, spans[0].TraceID)
		assert.Equal(t, "undefined", spans[0].ParentID)
	})

	t.Run("child-inherits-reconstructed-parent-trace", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		// Parent reconstructed with an explicit trace ID (left unfinished so only
		// the child is emitted); a child with no ID options should inherit it.
		parent, parentCtx := llmobs.StartWorkflowSpan(ctx, "reconstructed-parent",
			llmobs.WithTraceID(explicitTraceID),
		)
		child, _ := llmobs.StartLLMSpan(parentCtx, "child-llm")
		child.Finish()

		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		childEvent := spans[0]
		// Child inherits the parent's reconstructed trace...
		assert.Equal(t, explicitTraceID, childEvent.TraceID)
		assert.Equal(t, parent.TraceID(), childEvent.TraceID)
		// ...and derives its parent_id from the parent's APM span ID.
		assert.Equal(t, parent.SpanID(), childEvent.ParentID)
	})

	t.Run("explicit-parent-id-takes-precedence-over-derived-parent", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		// A real in-process parent exists (left unfinished so only the child is
		// emitted), but an explicit parent ID should win.
		parent, parentCtx := llmobs.StartWorkflowSpan(ctx, "parent-workflow")
		child, _ := llmobs.StartLLMSpan(parentCtx, "child-llm",
			llmobs.WithParentID(explicitParent),
		)
		child.Finish()

		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		childEvent := spans[0]
		assert.Equal(t, "child-llm", childEvent.Name)
		// The explicit parent_id replaces the derived in-process parent...
		assert.Equal(t, explicitParent, childEvent.ParentID)
		assert.NotEqual(t, parent.SpanID(), childEvent.ParentID)
		// ...while the child still inherits the parent's trace, keeping the tree coherent.
		assert.Equal(t, parent.TraceID(), childEvent.TraceID)
	})

	t.Run("no-options-generates-ids-unchanged", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartLLMSpan(ctx, "generated-llm")
		span.Finish()

		assert.NotEmpty(t, span.SpanID())
		assert.NotEmpty(t, span.TraceID())

		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.NotEmpty(t, spans[0].SpanID)
		assert.NotEmpty(t, spans[0].TraceID)
		// Parent is the "undefined" sentinel with no parent/propagated context.
		assert.Equal(t, "undefined", spans[0].ParentID)
		// Trace ID is the generated 32-hex form, not a chosen value.
		assert.Len(t, spans[0].TraceID, 32)
	})
}
