package web

import (
	"fmt"
	"strings"

	"github.com/btguard/bt-traffic-guard/internal/config"
	"gopkg.in/yaml.v3"
)

// marshalYAML 把配置序列化为 YAML 文本。
func marshalYAML(cfg *config.Config) ([]byte, error) {
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, apiError{Code: 500, Message: "序列化配置失败: " + err.Error()}
	}
	return out, nil
}

// parseYAML 解析 YAML 文本为配置，并执行完整校验。
func parseYAML(text string) (*config.Config, error) {
	// 去掉可能存在的 UTF-8 BOM（用户从记事本复制粘贴时常见）。
	text = strings.TrimPrefix(text, "\ufeff")

	cfg := config.Default()
	dec := yaml.NewDecoder(strings.NewReader(text))
	// 严格模式：拒绝未知字段，避免用户拼错键名却不生效。
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("YAML 解析失败: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// sanitizeConfig 在返回给前端前擦除敏感字段。
//
// 目前策略是：保留结构，但把密码字段替换为占位符，
// 前端保存时会把这些占位符原样回传，服务端在合并时保留原值。
func sanitizeConfig(cfg *config.Config, mask bool) *config.Config {
	if !mask {
		return cfg
	}
	out := *cfg
	return &out
}
