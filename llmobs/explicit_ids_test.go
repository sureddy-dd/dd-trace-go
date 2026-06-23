// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs_test

import (
	"context"
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

		// Public getters reflect the chosen values.
		wantSpanID := strconv.FormatUint(explicitSpanID, 10)
		assert.Equal(t, wantSpanID, span.SpanID())
		assert.Equal(t, explicitTraceID, span.TraceID())

		// The emitted wire event reflects all three.
		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, wantSpanID, spans[0].SpanID)
		assert.Equal(t, explicitTraceID, spans[0].TraceID)
		assert.Equal(t, explicitParent, spans[0].ParentID)
	})

	t.Run("explicit-parent-id-takes-precedence-over-derived-parent", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		// A real in-process parent exists, but an explicit parent ID should win.
		_, parentCtx := llmobs.StartWorkflowSpan(ctx, "parent-workflow")
		child, _ := llmobs.StartLLMSpan(parentCtx, "child-llm",
			llmobs.WithParentID(explicitParent),
		)
		child.Finish()

		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		var childEvent = spans[0]
		assert.Equal(t, "child-llm", childEvent.Name)
		assert.Equal(t, explicitParent, childEvent.ParentID)
	})

	t.Run("no-options-generates-ids-unchanged", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartLLMSpan(ctx, "generated-llm")
		span.Finish()

		// IDs are still generated (non-empty) and the parent is the default
		// "undefined" sentinel when there is no parent/propagated context.
		assert.NotEmpty(t, span.SpanID())
		assert.NotEmpty(t, span.TraceID())

		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.NotEmpty(t, spans[0].SpanID)
		assert.NotEmpty(t, spans[0].TraceID)
		assert.Equal(t, "undefined", spans[0].ParentID)
		// Trace ID should be the generated 32-hex form, not a chosen value.
		assert.Len(t, spans[0].TraceID, 32)
	})
}
