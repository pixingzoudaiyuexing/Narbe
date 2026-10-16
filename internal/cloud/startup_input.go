package cloud

// StartupInput deliberately exposes only initialization fields on advanced
// actions; it cannot smuggle a second image/network/firewall identity.
type StartupInput struct {
	ScriptMode             string            `json:"scriptMode,omitempty"`
	UserData               string            `json:"userData,omitempty"`
	ScriptCompatibilityAck bool              `json:"scriptCompatibilityAck,omitempty"`
	StackScriptID          int               `json:"stackscriptId,omitempty"`
	StackScriptData        map[string]string `json:"stackscriptData,omitempty"`
	StackScriptSnapshot    string            `json:"stackscriptSnapshot,omitempty"`
	StackScriptName        string            `json:"stackscriptName,omitempty"`
}

func (s *StartupInput) Spec(image, region string) Spec {
	if s == nil {
		return Spec{Image: image, Region: region}
	}
	return Spec{Image: image, Region: region, ScriptMode: s.ScriptMode, UserData: s.UserData, ScriptCompatibilityAck: s.ScriptCompatibilityAck, StackScriptID: s.StackScriptID, StackScriptData: s.StackScriptData, StackScriptSnapshot: s.StackScriptSnapshot, StackScriptName: s.StackScriptName}
}
