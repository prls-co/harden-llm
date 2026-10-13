package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-422

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOpenAIContractCompositionOwner(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "..", "..", "cmd", "harden-llm-gateway", "server.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	composed := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		owner, ok := selector.X.(*ast.Ident)
		if ok && owner.Name == "stateful" && (selector.Sel.Name == "NewClient" || selector.Sel.Name == "NewService") {
			composed = true
		}
		return true
	})
	if !composed {
		t.Error("gateway executable must construct the one stateful provider/observer composition instead of the retired static constructor")
	}
}

func TestOpenAIContractIndependentOfApplicationServices(t *testing.T) {
	// Library construction does not read deployment settings or connect to these
	// unavailable services. The HTTP-only fixture still uses the real provider codec.
	t.Setenv("HARDEN_LLM_DATABASE_URL", "postgresql://fixture@127.0.0.1:1/unavailable")
	t.Setenv("HARDEN_LLM_CONTROL_PLANE_URL", "http://127.0.0.1:1/unavailable")
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"model-a","object":"model","owned_by":"fixture"}]}`)
			return
		}
		calls.Add(1)
		_, _ = io.WriteString(w, `{"id":"fixture","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)
	}))
	defer upstream.Close()
	proxy := newContractProxy(t, upstream)
	defer proxy.Close()
	request, err := http.NewRequest(http.MethodPost, proxy.URL+"/v1/responses", strings.NewReader(`{"model":"model-a","input":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+proxyTestToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := proxy.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "ok") || calls.Load() != 1 {
		t.Fatalf("ordinary bearer-only call depended on app services: status=%d calls=%d body=%s", response.StatusCode, calls.Load(), body)
	}
}
