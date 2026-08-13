package doc2x

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/labring/aiproxy/core/relay/meta"
)

func TestIsSuccessfulResponseCode(t *testing.T) {
	t.Parallel()

	for _, code := range []string{"success", "ok"} {
		if !isSuccessfulResponseCode(code) {
			t.Errorf("expected %q to be accepted", code)
		}
	}

	if isSuccessfulResponseCode("failed") {
		t.Fatal("failed response code was accepted")
	}
}

func TestJoinMarkdownPagesPreservesPageBoundary(t *testing.T) {
	t.Parallel()

	pages := []string{"第一张页面", "第二张页面"}

	result := joinMarkdownPages(pages)
	if result != "第一张页面\n\n第二张页面" {
		t.Fatalf("expected explicit page boundary, got %q", result)
	}
}

func TestHandleParsePdfResponsePollsReadyUntilSuccess(t *testing.T) {
	oldInterval := statusPollInterval
	statusPollInterval = 0
	t.Cleanup(func() { statusPollInterval = oldInterval })

	var calls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++

		w.Header().Set("Content-Type", "application/json")

		if calls == 1 {
			_, _ = io.WriteString(w, `{"code":"success","data":{"status":"ready","progress":1}}`)
			return
		}

		_, _ = io.WriteString(
			w,
			`{"code":"success","data":{"status":"success","result":{"pages":[{"md":"正文"}]}}}`,
		)
	}))
	t.Cleanup(server.Close)

	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)

	m := &meta.Meta{
		Channel:        meta.ChannelMeta{BaseURL: server.URL},
		RequestTimeout: time.Second,
	}

	response := &http.Response{
		Body: io.NopCloser(strings.NewReader(`{"code":"success","data":{"uid":"test-uid"}}`)),
	}
	if _, err := HandleParsePdfResponse(m, ctx, response); err != nil {
		t.Fatalf("expected ready status to continue polling: %v", err)
	}

	if calls != 2 {
		t.Fatalf("expected two status requests, got %d", calls)
	}

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected successful JSON response, got %d", recorder.Code)
	}
}
