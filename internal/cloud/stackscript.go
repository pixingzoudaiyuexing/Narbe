package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type StackScriptProvider interface {
	StackScripts(context.Context, string, string, int) (StackScriptPage, error)
	StackScript(context.Context, int) (StackScript, error)
	ValidateStackScript(context.Context, Spec) error
}
type StackField struct {
	Name    string  `json:"name"`
	Label   string  `json:"label"`
	Example string  `json:"example"`
	Default *string `json:"default"`
	OneOf   string  `json:"oneOf,omitempty"`
	ManyOf  string  `json:"manyOf,omitempty"`
}
type StackScript struct {
	ID          int          `json:"id"`
	Label       string       `json:"label"`
	Description string       `json:"description"`
	Username    string       `json:"username"`
	Mine        bool         `json:"mine"`
	Public      bool         `json:"is_public"`
	Images      []string     `json:"images"`
	Fields      []StackField `json:"user_defined_fields"`
	Updated     string       `json:"updated"`
	Snapshot    string       `json:"snapshot"`
}
type stackResponse struct {
	StackScript
	Script string `json:"script"`
}
type StackScriptPage struct {
	Items   []StackScript `json:"items"`
	Page    int           `json:"page"`
	Pages   int           `json:"pages"`
	Results int           `json:"results"`
}

func (l *Linode) StackScripts(ctx context.Context, query, scope string, page int) (StackScriptPage, error) {
	v := StackScriptPage{}
	if page < 1 || page > 100 || len(query) > 128 || !utf8.ValidString(query) {
		return v, errors.New("StackScript搜索或分页无效")
	}
	filters := map[string]any{}
	switch scope {
	case "mine":
		filters["mine"] = true
	case "public":
		filters["is_public"] = true
	case "accessible":
	default:
		return v, errors.New("请选择我的、公共或可访问StackScripts")
	}
	if query != "" {
		filters["label"] = map[string]string{"+contains": query}
	}
	raw, _ := json.Marshal(filters)
	var res struct {
		Data                 []StackScript
		Page, Pages, Results int
	}
	if _, e := l.request(ctx, "GET", fmt.Sprintf("/linode/stackscripts?page=%d&page_size=25", page), nil, &res, string(raw)); e != nil {
		return v, e
	}
	if res.Data == nil || res.Page != page || res.Pages < 1 || res.Page > res.Pages || len(res.Data) > 25 || res.Results < 0 {
		return v, &Error{Code: "StackScript分页响应不完整"}
	}
	for _, s := range res.Data {
		if s.ID < 1 || s.Label == "" || len(s.Fields) > 100 {
			return v, &Error{Code: "StackScript身份或参数定义无效"}
		}
		v.Items = append(v.Items, s)
	}
	if v.Items == nil {
		v.Items = []StackScript{}
	}
	v.Page = res.Page
	v.Pages = res.Pages
	v.Results = res.Results
	return v, nil
}
func (l *Linode) StackScript(ctx context.Context, id int) (StackScript, error) {
	if id < 1 {
		return StackScript{}, errors.New("StackScript ID无效")
	}
	var s stackResponse
	if e := l.call(ctx, "GET", "/linode/stackscripts/"+strconv.Itoa(id), nil, &s); e != nil {
		return StackScript{}, e
	}
	if s.ID != id || s.Label == "" || s.Script == "" || s.Images == nil || s.Fields == nil || len(s.Fields) > 100 {
		return StackScript{}, &Error{Code: "StackScript身份、脚本或UDF响应不完整"}
	}
	// Hash source and all execution-relevant definition fields; never return source.
	s.Snapshot = fingerprint(struct {
		ID                               int
		Label, Username, Updated, Script string
		Mine, Public                     bool
		Images                           []string
		Fields                           []StackField
	}{s.ID, s.Label, s.Username, s.Updated, s.Script, s.Mine, s.Public, s.Images, s.Fields})
	return s.StackScript, nil
}

var stackFieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func ValidateStackFields(fields []StackField, values map[string]string) error {
	allowed := map[string]bool{}
	for _, f := range fields {
		if !stackFieldName.MatchString(f.Name) || allowed[f.Name] || len(f.Label) > 2048 || (f.OneOf != "" && f.ManyOf != "") {
			return errors.New("StackScript参数定义无效或重复，请检查云控制台")
		}
		allowed[f.Name] = true
		value, present := values[f.Name]
		if !present && f.Default != nil {
			value = *f.Default
			present = true
		}
		if !present || (value == "" && f.Default == nil) {
			return errors.New("缺少StackScript必填参数，请检查表单")
		}
		if f.OneOf != "" && !contains(strings.Split(f.OneOf, ","), value) {
			return errors.New("StackScript单选参数不在官方允许范围内")
		}
		if f.ManyOf != "" && value != "" {
			seen := map[string]bool{}
			for _, part := range strings.Split(value, ",") {
				if seen[part] || !contains(strings.Split(f.ManyOf, ","), part) {
					return errors.New("StackScript多选参数不在官方允许范围内或重复")
				}
				seen[part] = true
			}
		}
	}
	for k := range values {
		if !allowed[k] {
			return errors.New("StackScript包含未声明参数，拒绝提交")
		}
	}
	return nil
}
func (l *Linode) ValidateStackScript(ctx context.Context, s Spec) error {
	if e := ValidateStartupText("linode", s); e != nil {
		return e
	}
	live, e := l.StackScript(ctx, s.StackScriptID)
	if e != nil {
		return e
	}
	if s.StackScriptSnapshot == "" || s.StackScriptSnapshot != live.Snapshot || s.StackScriptName != live.Label {
		return errors.New("StackScript身份、版本或参数定义已变化，请重新选择并确认")
	}
	if !contains(live.Images, s.Image) && !contains(live.Images, "any/all") {
		return errors.New("StackScript不支持当前系统镜像")
	}
	return ValidateStackFields(live.Fields, s.StackScriptData)
}
