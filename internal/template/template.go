package template

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"time"
)

// MessageData 是渲染 Topic 模板时可用的变量集合
type MessageData struct {
	// 消息 ID（Gotify 分配）
	ID uint
	// 应用 ID（发送该消息的 Gotify Application）
	AppID uint
	// 当前连接用户的 ID（Token 所属用户，连接时解析）
	UserID uint
	// 当前连接用户名
	UserName string
	// 消息标题（已做 Topic 安全处理：特殊字符替换）
	Title string
	// 消息优先级
	Priority int
	// 消息创建时间
	Date time.Time
}

// topicUnsafe 匹配 MQTT Topic 中不允许出现在发布路径中的字符
var topicUnsafe = regexp.MustCompile(`[+#]`)

// SanitizeTopic 对字符串做 MQTT Topic 安全处理：
//   - 空白字符 → '_'
//   - '+' / '#'（通配符）→ 删除
//   - 保留 '/' 作为层级分隔符
func SanitizeTopic(s string) string {
	// 先处理空白
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return '_'
		}
		return r
	}, s)
	// 删除通配符
	return topicUnsafe.ReplaceAllString(s, "")
}

// RenderTopics 渲染一批 Topic 模板字符串，返回渲染结果列表。
// data.Title 会在渲染前自动做 Topic 安全处理。
func RenderTopics(templates []string, data MessageData) ([]string, error) {
	// 安全处理 Title，其他字段由调用方保证
	data.Title = SanitizeTopic(data.Title)

	results := make([]string, 0, len(templates))
	for _, tmplStr := range templates {
		t, err := template.New("topic").Parse(tmplStr)
		if err != nil {
			return nil, fmt.Errorf("解析 topic 模板 %q: %w", tmplStr, err)
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("渲染 topic 模板 %q: %w", tmplStr, err)
		}
		rendered := buf.String()
		// 最终结果再过一遍安全处理（防止变量值引入非法字符）
		rendered = topicUnsafe.ReplaceAllString(rendered, "")
		results = append(results, rendered)
	}
	return results, nil
}
