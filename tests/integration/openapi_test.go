package integration

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// openAPIDoc 是 api/openapi.yaml 的最小结构，用于覆盖性校验。
type openAPIDoc struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Title   string `yaml:"title"`
		Version string `yaml:"version"`
	} `yaml:"info"`
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		SecuritySchemes map[string]any `yaml:"securitySchemes"`
	} `yaml:"components"`
}

// loadOpenAPI 解析接口文档。
func loadOpenAPI(t *testing.T) *openAPIDoc {
	t.Helper()

	raw, err := os.ReadFile("../../api/openapi.yaml")
	require.NoError(t, err, "接口文档必须存在于 api/openapi.yaml")

	var doc openAPIDoc
	require.NoError(t, yaml.Unmarshal(raw, &doc), "接口文档必须是合法的 YAML")
	return &doc
}

// TestOpenAPIIsValid 校验文档的基本元信息。
func TestOpenAPIIsValid(t *testing.T) {
	doc := loadOpenAPI(t)

	assert.True(t, strings.HasPrefix(doc.OpenAPI, "3.0"), "必须声明 OpenAPI 3.0 版本，实际为 %q", doc.OpenAPI)
	assert.NotEmpty(t, doc.Info.Title)
	assert.NotEmpty(t, doc.Info.Version)
	assert.Contains(t, doc.Components.SecuritySchemes, "bearerAuth", "必须声明 Bearer 认证方案")
}

// ginPathParam 匹配 Gin 的路径参数写法，例如 :tenantID。
var ginPathParam = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

// apiPrefix 是文档中通过 servers 声明的公共前缀。
const apiPrefix = "/api/v1"

// toOpenAPIPath 把 Gin 路由路径转换为文档中的写法：
// 1. 去掉 servers 声明的公共前缀 `/api/v1`；
// 2. 路径参数由 `:tenantID` 改写为 `{tenantID}`。
func toOpenAPIPath(ginPath string) string {
	path := strings.TrimPrefix(ginPath, apiPrefix)
	if path == "" {
		path = "/"
	}
	return ginPathParam.ReplaceAllString(path, "{$1}")
}

// TestOpenAPICoversEveryRoute 校验文档覆盖全部已注册路由。
//
// 这是文档防腐化的核心机制：新增路由却忘记更新文档时，
// 本用例会直接失败，而不是让文档静默过期。
func TestOpenAPICoversEveryRoute(t *testing.T) {
	doc := loadOpenAPI(t)
	stack := newTenancyStack(t)

	var missing []string
	for _, route := range stack.router.Routes() {
		path := toOpenAPIPath(route.Path)
		item, ok := doc.Paths[path]
		if !ok {
			missing = append(missing, route.Method+" "+route.Path+"（文档缺少 path "+path+"）")
			continue
		}
		if _, ok := item[strings.ToLower(route.Method)]; !ok {
			missing = append(missing, route.Method+" "+route.Path+"（文档缺少该方法）")
		}
	}

	assert.Empty(t, missing,
		"以下路由未在 api/openapi.yaml 中登记：\n%s", strings.Join(missing, "\n"))
}

// TestOpenAPIHasNoStalePaths 校验文档中没有已不存在的路由。
//
// 反向校验同样重要：否则删除路由后文档会残留幽灵端点。
func TestOpenAPIHasNoStalePaths(t *testing.T) {
	doc := loadOpenAPI(t)
	stack := newTenancyStack(t)

	registered := make(map[string]struct{})
	for _, route := range stack.router.Routes() {
		registered[route.Method+" "+toOpenAPIPath(route.Path)] = struct{}{}
	}

	var stale []string
	for path, item := range doc.Paths {
		for method := range item {
			switch method {
			case "parameters", "summary", "description":
				continue
			}
			key := strings.ToUpper(method) + " " + path
			if _, ok := registered[key]; !ok {
				stale = append(stale, key)
			}
		}
	}

	assert.Empty(t, stale,
		"api/openapi.yaml 中存在未注册的端点：\n%s", strings.Join(stale, "\n"))
}
