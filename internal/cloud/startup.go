package cloud

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Startup data remains plain UTF-8 until the provider performs exactly one
// transport encoding. It is never interpreted by the Narbe process.
const StartupNotice = "初始化脚本已提交，实际执行结果请登录服务器检查。"

var shebang = regexp.MustCompile(`^#![ \t]*/[^\r\n]+(?:\r?\n|$)`)

func StartupLimit(provider string) int {
	switch provider {
	case "aws":
		return 16384
	case "azure":
		return 65535
	case "digitalocean":
		return 65536
	case "linode":
		return 49149
	}
	return 0
}
func StartupPayload(s Spec) string {
	if s.ScriptMode == "powershell" {
		return "<powershell>\n" + s.UserData + "\n</powershell>"
	}
	return s.UserData
}
func ValidateStartupText(provider string, s Spec) error {
	mode := s.ScriptMode
	if mode == "" {
		mode = "none"
	}
	if mode == "none" {
		if s.UserData != "" || s.StackScriptID != 0 || len(s.StackScriptData) > 0 || s.StackScriptSnapshot != "" || s.StackScriptName != "" || s.ScriptCompatibilityAck {
			return errors.New("未启用初始化时不能附带脚本、StackScript或兼容性确认")
		}
		return nil
	}
	if mode == "stackscript" {
		if provider != "linode" || s.UserData != "" || s.StackScriptID < 1 || s.ScriptCompatibilityAck {
			return errors.New("StackScript仅限Linode，且不能与Cloud-init组合")
		}
		if len(s.StackScriptData) > 100 {
			return errors.New("StackScript参数超过100项")
		}
		for k, v := range s.StackScriptData {
			if len(k) > 128 || len(v) > 32768 || !utf8.ValidString(k) || !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
				return errors.New("StackScript参数无效或过长")
			}
		}
		raw, _ := json.Marshal(s.StackScriptData)
		if len(raw) > 65535 {
			return errors.New("StackScript参数JSON超过65535字节")
		}
		return nil
	}
	if s.StackScriptID != 0 || len(s.StackScriptData) > 0 || s.StackScriptSnapshot != "" || s.StackScriptName != "" {
		return errors.New("文本初始化不能同时携带StackScript参数")
	}
	if mode != "shell" && mode != "cloud-config" && !(mode == "powershell" && provider == "aws") {
		return errors.New("初始化模式不受该云厂商支持")
	}
	if !utf8.ValidString(s.UserData) || strings.ContainsRune(s.UserData, 0) || strings.TrimSpace(s.UserData) == "" {
		return errors.New("初始化脚本需要非空有效UTF-8文本，不能包含空字节")
	}
	limit := StartupLimit(provider)
	if limit == 0 || len(StartupPayload(s)) > limit {
		return fmt.Errorf("初始化UTF-8字节超限（本模式最多%d字节，包含必要格式）", limit)
	}
	switch mode {
	case "shell":
		if !shebang.MatchString(s.UserData) {
			return errors.New("Shell脚本必须以合法#!/解释器开头；请粘贴原始文本，不要预先Base64编码")
		}
	case "cloud-config":
		if !strings.HasPrefix(s.UserData, "#cloud-config\n") && !strings.HasPrefix(s.UserData, "#cloud-config\r\n") {
			return errors.New("Cloud-config必须以#cloud-config独占首行开头，不接受预编码文本")
		}
	case "powershell":
		if decoded, e := base64.StdEncoding.DecodeString(strings.TrimSpace(s.UserData)); e == nil && utf8.Valid(decoded) && (strings.HasPrefix(string(decoded), "<powershell>") || strings.HasPrefix(string(decoded), "#!") || strings.HasPrefix(string(decoded), "#cloud-config")) {
			return errors.New("请填写原始PowerShell文本，不要Base64编码")
		}
		lower := strings.ToLower(s.UserData)
		if strings.HasPrefix(s.UserData, "#!") || strings.HasPrefix(s.UserData, "#cloud-config") || strings.Contains(lower, "<powershell") || strings.Contains(lower, "</powershell") || strings.Contains(lower, "<persist>") || strings.Contains(lower, "<script>") {
			return errors.New("PowerShell请填写原始命令，不要添加XML标签、Linux脚本或持久执行设置")
		}
	}
	return nil
}
func ValidateStartup(provider string, s Spec, c Catalog) error {
	if e := ValidateStartupText(provider, s); e != nil {
		return e
	}
	if s.ScriptMode == "" || s.ScriptMode == "none" {
		return nil
	}
	image, ok := Find(c.Images, s.Image)
	if !ok {
		return errors.New("脚本目标镜像不存在，请重新选择")
	}
	windows := image.Family == "windows"
	if windows {
		if provider != "aws" || s.ScriptMode != "powershell" {
			return errors.New("当前Windows镜像不支持该初始化方式；Azure customData不会自动执行")
		}
	} else if s.ScriptMode == "powershell" {
		return errors.New("PowerShell初始化仅用于AWS Windows镜像")
	}
	if s.ScriptMode == "stackscript" {
		region, _ := Find(c.Regions, s.Region)
		if region.Extra["siteType"] == "distributed" {
			return errors.New("StackScripts不支持distributed地区")
		}
		return nil
	}
	if provider == "linode" {
		region, _ := Find(c.Regions, s.Region)
		if image.StartupSupport != "cloud-init" || region.Extra["metadata"] != "true" {
			return errors.New("所选镜像或地区未提供Cloud-init / Metadata能力，不能使用初始化脚本")
		}
		if s.ScriptCompatibilityAck {
			return errors.New("Linode镜像能力已由API核实，不接受绕过确认")
		}
		return nil
	}
	if !s.ScriptCompatibilityAck {
		return errors.New("请确认已核实镜像包含cloud-init或受支持EC2Launch代理；Narbe不能保证脚本执行结果")
	}
	return nil
}

type StartupSummary struct {
	Mode                string `json:"mode"`
	Bytes               int    `json:"bytes"`
	SHA256              string `json:"sha256"`
	StackScriptID       int    `json:"stackscriptId,omitempty"`
	StackScriptName     string `json:"stackscriptName,omitempty"`
	StackScriptSnapshot string `json:"stackscriptSnapshot,omitempty"`
	Execution           string `json:"execution"`
}

func SummarizeStartup(s Spec) *StartupSummary {
	if s.ScriptMode == "" || s.ScriptMode == "none" {
		return nil
	}
	data := []byte(s.UserData)
	if s.ScriptMode == "stackscript" {
		data, _ = json.Marshal(s.StackScriptData)
	}
	sum := sha256.Sum256(data)
	return &StartupSummary{Mode: s.ScriptMode, Bytes: len(data), SHA256: hex.EncodeToString(sum[:]), StackScriptID: s.StackScriptID, StackScriptName: s.StackScriptName, StackScriptSnapshot: s.StackScriptSnapshot, Execution: "not-confirmed"}
}
