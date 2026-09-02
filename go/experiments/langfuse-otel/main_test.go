// 本文件验证 Agent 执行 seam 的观测树，以及队列边界上的 Trace Context 传播契约。
// 测试使用内存 exporter，不需要 Langfuse 凭证，也不会访问网络。
package main

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// TestTraceContextSurvivesJSONQueue 模拟“生产者入队”和“消费者出队”两个进程。
func TestTraceContextSurvivesJSONQueue(t *testing.T) {
	// 测试使用没有 exporter 的 SDK provider，避免单元测试真的访问网络。
	provider := sdktrace.NewTracerProvider()
	// Shutdown 即使当前没有导出任务也需要显式调用，保持真实程序的资源生命周期习惯。
	defer func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown provider: %v", err)
		}
	}()

	// 保存并恢复全局 provider/propagator，避免这个测试影响同一进程中的其他测试。
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer otel.SetTracerProvider(previousProvider)
	defer otel.SetTextMapPropagator(previousPropagator)

	// 创建一个 enqueue Span，只有存在有效 Span Context 时才有可传播的 Trace ID。
	ctx, span := otel.Tracer("test").Start(context.Background(), "enqueue")
	defer span.End()

	// marshal 模拟生产者把传播信息写入队列消息。
	payload, err := marshalTracePayload(ctx)
	if err != nil {
		t.Fatalf("marshal trace payload: %v", err)
	}

	// unmarshal 模拟消费者从队列消息恢复 Context。
	workerCtx, err := unmarshalTraceContext(payload)
	if err != nil {
		t.Fatalf("unmarshal trace payload: %v", err)
	}

	// 空 carrier 提取不会改变已经恢复的 Context；真正的断言比较恢复后的 Trace ID。
	got := propagation.TraceContext{}.Extract(workerCtx, propagation.MapCarrier{})
	if got != workerCtx {
		t.Fatalf("extracted context changed unexpectedly")
	}
	if gotSpan := oteltrace.SpanContextFromContext(workerCtx); gotSpan.TraceID() != span.SpanContext().TraceID() {
		t.Fatalf("trace id = %s, want %s", gotSpan.TraceID(), span.SpanContext().TraceID())
	}
}

// TestUnmarshalTraceContextRejectsMissingTraceparent 验证坏消息不会静默生成错误链路。
func TestUnmarshalTraceContextRejectsMissingTraceparent(t *testing.T) {
	// 缺少传播字段时，消费者应立即返回错误，而不是创建孤立 Span。
	if _, err := unmarshalTraceContext([]byte(`{}`)); err == nil {
		t.Fatal("expected missing traceparent error")
	}
}

// TestReactAgentTrace 验证学习者在 Langfuse 中应看到的完整两轮执行树。
func TestReactAgentTrace(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown provider: %v", err)
		}
	}()

	answer, err := newReactAgent().execute(context.Background(), provider.Tracer("test"))
	if err != nil {
		t.Fatalf("execute agent: %v", err)
	}
	if answer != finalAnswer {
		t.Fatalf("answer = %q, want %q", answer, finalAnswer)
	}

	spans := spansByName(t, exporter.GetSpans())
	wantParents := map[string]string{
		"agent.round.1":               "agent.execute",
		"agent.generation.1":          "agent.round.1",
		"agent.tool.knowledge_search": "agent.round.1",
		"retrieve":                    "agent.tool.knowledge_search",
		"agent.round.2":               "agent.execute",
		"agent.generation.2":          "agent.round.2",
	}
	if len(spans) != len(wantParents)+1 {
		t.Fatalf("span count = %d, want %d", len(spans), len(wantParents)+1)
	}

	root := spans["agent.execute"]
	if root.Name == "" {
		t.Fatal("agent.execute span is missing")
	}
	wantTypes := map[string]string{
		"agent.execute":               "agent",
		"agent.round.1":               "chain",
		"agent.generation.1":          "generation",
		"agent.tool.knowledge_search": "tool",
		"retrieve":                    "retriever",
		"agent.round.2":               "chain",
		"agent.generation.2":          "generation",
	}
	for name, wantType := range wantTypes {
		span := spans[name]
		if span.Name == "" {
			t.Errorf("span %q is missing", name)
			continue
		}
		if span.SpanContext.TraceID() != root.SpanContext.TraceID() {
			t.Errorf("%s trace id = %s, want %s", name, span.SpanContext.TraceID(), root.SpanContext.TraceID())
		}
		if got := spanAttribute(span, "langfuse.observation.type").AsString(); got != wantType {
			t.Errorf("%s observation type = %q, want %q", name, got, wantType)
		}
	}
	for childName, parentName := range wantParents {
		if got, want := spans[childName].Parent.SpanID(), spans[parentName].SpanContext.SpanID(); got != want {
			t.Errorf("%s parent = %s, want %s (%s)", childName, got, want, parentName)
		}
	}

	firstGeneration := spans["agent.generation.1"]
	assertGenerationAttributes(t, firstGeneration, "tool_calls", 18, 12, 30)
	if got := spanAttribute(firstGeneration, "gen_ai.completion").AsString(); !strings.Contains(got, "knowledge_search") {
		t.Errorf("first completion %q does not express the tool call", got)
	}
	secondGeneration := spans["agent.generation.2"]
	assertGenerationAttributes(t, secondGeneration, "stop", 32, 16, 48)
	if got := spanAttribute(secondGeneration, "gen_ai.prompt").AsString(); !strings.Contains(got, knowledgeResult) {
		t.Errorf("second prompt %q does not contain the tool result", got)
	}
	if got := spanAttribute(secondGeneration, "gen_ai.completion").AsString(); !strings.Contains(got, finalAnswer) {
		t.Errorf("second completion %q does not contain the final answer", got)
	}

	tool := spans["agent.tool.knowledge_search"]
	if got := spanAttribute(tool, "tool.status").AsString(); got != "success" {
		t.Errorf("tool status = %q, want success", got)
	}
	if got := spanAttribute(tool, "langfuse.observation.input").AsString(); got != `{"query":"Go context cancellation"}` {
		t.Errorf("tool input = %q", got)
	}
	if got := spanAttribute(tool, "langfuse.observation.output").AsString(); got != knowledgeResult {
		t.Errorf("tool output = %q, want %q", got, knowledgeResult)
	}
}

// TestReactAgentErrors 验证错误会返回调用方，并且已创建的 Span 都被结束和导出。
func TestReactAgentErrors(t *testing.T) {
	tests := []struct {
		name       string
		agent      reactAgent
		wantError  string
		wantSpans  int
		failedSpan string
	}{
		{name: "invalid tool input", agent: reactAgent{maxRounds: 3, toolInput: `{"query":""}`}, wantError: "tool input is empty", wantSpans: 4, failedSpan: "agent.tool.knowledge_search"},
		{name: "tool failure", agent: reactAgent{maxRounds: 3, toolInput: defaultToolInput, failTool: true}, wantError: "knowledge search failed", wantSpans: 4, failedSpan: "agent.tool.knowledge_search"},
		{name: "maximum rounds", agent: reactAgent{maxRounds: 1, toolInput: defaultToolInput}, wantError: "maximum rounds reached", wantSpans: 5, failedSpan: "agent.execute"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
			defer func() {
				if err := provider.Shutdown(context.Background()); err != nil {
					t.Fatalf("shutdown provider: %v", err)
				}
			}()

			if _, err := tt.agent.execute(context.Background(), provider.Tracer("test")); err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantError)
			}
			spans := spansByName(t, exporter.GetSpans())
			if len(spans) != tt.wantSpans {
				t.Fatalf("ended span count = %d, want %d", len(spans), tt.wantSpans)
			}
			if got := spans[tt.failedSpan].Status.Code; got != codes.Error {
				t.Errorf("%s status = %v, want error", tt.failedSpan, got)
			}
		})
	}
}

func spansByName(t *testing.T, spans tracetest.SpanStubs) map[string]tracetest.SpanStub {
	t.Helper()
	byName := make(map[string]tracetest.SpanStub, len(spans))
	for _, span := range spans {
		if _, exists := byName[span.Name]; exists {
			t.Fatalf("duplicate span name %q", span.Name)
		}
		byName[span.Name] = span
	}
	return byName
}

func spanAttribute(span tracetest.SpanStub, key attribute.Key) attribute.Value {
	for _, value := range span.Attributes {
		if value.Key == key {
			return value.Value
		}
	}
	return attribute.Value{}
}

func assertGenerationAttributes(t *testing.T, span tracetest.SpanStub, finishReason string, inputTokens, outputTokens, totalTokens int64) {
	t.Helper()
	if got := spanAttribute(span, "gen_ai.request.model").AsString(); got != fakeModelName {
		t.Errorf("model = %q, want %q", got, fakeModelName)
	}
	if got := spanAttribute(span, "gen_ai.operation.name").AsString(); got != "chat" {
		t.Errorf("operation = %q, want chat", got)
	}
	if got := spanAttribute(span, "gen_ai.prompt").AsString(); got == "" {
		t.Error("prompt is empty")
	}
	if got := spanAttribute(span, "gen_ai.completion").AsString(); got == "" {
		t.Error("completion is empty")
	}
	if got := spanAttribute(span, "gen_ai.response.finish_reasons").AsStringSlice(); len(got) != 1 || got[0] != finishReason {
		t.Errorf("finish reasons = %v, want [%s]", got, finishReason)
	}
	for key, want := range map[attribute.Key]int64{
		"gen_ai.usage.input_tokens":  inputTokens,
		"gen_ai.usage.output_tokens": outputTokens,
		"gen_ai.usage.total_tokens":  totalTokens,
	} {
		if got := spanAttribute(span, key).AsInt64(); got != want {
			t.Errorf("%s = %d, want %d", key, got, want)
		}
	}
}
