// Package rule 提供高频问题的正则规则库匹配能力。
package rule

import (
	"regexp"
	"strings"
	"sync"
)

// Rule 是一条高频问题规则：pattern 为正则表达式，answer 为预设答案。
type Rule struct {
	ID      int64
	Pattern string
	Answer  string
}

// Library 是可并发使用的规则库，按加入顺序（通常为 id 顺序）取首个命中。
type Library struct {
	mu     sync.RWMutex
	rules  []Rule
	regexs []*regexp.Regexp
}

// NewLibrary 编译并装载规则；遇到非法正则跳过该条（调用方应在校验入口拦截）。
func NewLibrary(rules []Rule) *Library {
	lib := &Library{}
	lib.Reload(rules)
	return lib
}

// Reload 原子替换全部规则。
func (l *Library) Reload(rules []Rule) {
	compiled := make([]*regexp.Regexp, 0, len(rules))
	kept := make([]Rule, 0, len(rules))
	for _, r := range rules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			continue
		}
		compiled = append(compiled, re)
		kept = append(kept, r)
	}
	l.mu.Lock()
	l.rules, l.regexs = kept, compiled
	l.mu.Unlock()
}

// Match 返回首个命中的规则；未命中返回 ok=false。
func (l *Library) Match(text string) (Rule, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for i, re := range l.regexs {
		if re.MatchString(text) {
			return l.rules[i], true
		}
	}
	return Rule{}, false
}

// Validate 检查 pattern 是否为合法正则且 answer 非空。
func Validate(pattern, answer string) bool {
	if strings.TrimSpace(pattern) == "" || strings.TrimSpace(answer) == "" {
		return false
	}
	_, err := regexp.Compile(pattern)
	return err == nil
}
