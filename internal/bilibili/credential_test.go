package bilibili

import (
	"testing"
	"time"
)

func TestCredentialFromCookieString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantNil  bool
		sessdata string
		biliJCT  string
		buvid3   string
		dedeUID  string
	}{
		{
			name:     "完整 Cookie",
			input:    "SESSDATA=abc123; bili_jct=def456; buvid3=xxx-yyy; DedeUserID=12345",
			sessdata: "abc123",
			biliJCT:  "def456",
			buvid3:   "xxx-yyy",
			dedeUID:  "12345",
		},
		{
			name:     "只有 SESSDATA",
			input:    "SESSDATA=abc123",
			sessdata: "abc123",
		},
		{
			name:    "空字符串",
			input:   "",
			wantNil: true,
		},
		{
			name:    "无 SESSDATA",
			input:   "bili_jct=def456; buvid3=xxx",
			wantNil: true,
		},
		{
			name:     "带空格和分号",
			input:    " SESSDATA = abc123 ; bili_jct = def456 ",
			sessdata: "abc123",
			biliJCT:  "def456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cred := CredentialFromCookieString(tt.input)
			if tt.wantNil {
				if cred != nil {
					t.Errorf("expected nil, got %+v", cred)
				}
				return
			}
			if cred == nil {
				t.Fatal("expected non-nil credential")
			}
			if cred.Sessdata != tt.sessdata {
				t.Errorf("sessdata: want %q, got %q", tt.sessdata, cred.Sessdata)
			}
			if cred.BiliJCT != tt.biliJCT {
				t.Errorf("bili_jct: want %q, got %q", tt.biliJCT, cred.BiliJCT)
			}
			if cred.Buvid3 != tt.buvid3 {
				t.Errorf("buvid3: want %q, got %q", tt.buvid3, cred.Buvid3)
			}
			if cred.DedeUserID != tt.dedeUID {
				t.Errorf("dedeuserid: want %q, got %q", tt.dedeUID, cred.DedeUserID)
			}
		})
	}
}

func TestCredentialToCookieString(t *testing.T) {
	cred := &Credential{
		Sessdata:   "abc123",
		BiliJCT:    "def456",
		Buvid3:     "xxx-yyy",
		DedeUserID: "12345",
	}
	s := cred.ToCookieString()
	if s == "" {
		t.Fatal("expected non-empty cookie string")
	}
	// 验证包含所有必须字段
	for _, key := range []string{"SESSDATA=abc123", "bili_jct=def456", "buvid3=xxx-yyy", "DedeUserID=12345"} {
		if !contains(s, key) {
			t.Errorf("cookie string missing %q: %s", key, s)
		}
	}
}

func TestCredentialJSON(t *testing.T) {
	cred := &Credential{
		Sessdata:    "abc123",
		BiliJCT:     "def456",
		Buvid3:      "xxx-yyy",
		DedeUserID:  "12345",
		ACTimeValue: "refresh_token_xxx",
		UpdatedAt:   time.Now().Unix(),
	}

	j := cred.ToJSON()
	if j == "" {
		t.Fatal("expected non-empty JSON")
	}

	parsed := CredentialFromJSON(j)
	if parsed == nil {
		t.Fatal("expected non-nil parsed credential")
	}
	if parsed.Sessdata != cred.Sessdata {
		t.Errorf("sessdata mismatch: want %q, got %q", cred.Sessdata, parsed.Sessdata)
	}
	if parsed.BiliJCT != cred.BiliJCT {
		t.Errorf("bili_jct mismatch")
	}
	if parsed.ACTimeValue != cred.ACTimeValue {
		t.Errorf("ac_time_value mismatch")
	}
}

func TestCredentialFromJSONEmpty(t *testing.T) {
	if c := CredentialFromJSON(""); c != nil {
		t.Error("expected nil for empty string")
	}
	if c := CredentialFromJSON("{}"); c != nil {
		t.Error("expected nil for empty object (no sessdata)")
	}
	if c := CredentialFromJSON("invalid"); c != nil {
		t.Error("expected nil for invalid JSON")
	}
}

func TestCredentialIsEmpty(t *testing.T) {
	var nilCred *Credential
	if !nilCred.IsEmpty() {
		t.Error("nil credential should be empty")
	}
	if !((&Credential{}).IsEmpty()) {
		t.Error("zero credential should be empty")
	}
	if (&Credential{Sessdata: "abc"}).IsEmpty() {
		t.Error("credential with sessdata should not be empty")
	}
}

func TestGetCorrespondPath(t *testing.T) {
	ts := int64(1700000000000) // 固定时间戳
	path, err := getCorrespondPath(ts)
	if err != nil {
		t.Fatalf("getCorrespondPath failed: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty correspond path")
	}
	// RSA 加密结果是 hex 字符串，长度应该是 256（1024-bit key 输出 128 bytes = 256 hex chars）
	if len(path) != 256 {
		t.Errorf("expected 256 hex chars, got %d", len(path))
	}
}

func TestNewClientWithCredential(t *testing.T) {
	// nil credential
	c1 := NewClientWithCredential(nil)
	if c1 == nil {
		t.Fatal("expected non-nil client for nil credential")
	}
	if c1.cookie != "" {
		t.Error("expected empty cookie for nil credential")
	}

	// empty credential
	c2 := NewClientWithCredential(&Credential{})
	if c2.cookie != "" {
		t.Error("expected empty cookie for empty credential")
	}

	// valid credential
	cred := &Credential{
		Sessdata:   "abc123",
		BiliJCT:    "def456",
		Buvid3:     "xxx-yyy",
		DedeUserID: "12345",
	}
	c3 := NewClientWithCredential(cred)
	if c3.credential == nil {
		t.Error("expected non-nil credential in client")
	}
	if c3.cookie == "" {
		t.Error("expected non-empty cookie string")
	}
	if !contains(c3.cookie, "SESSDATA=abc123") {
		t.Error("cookie should contain SESSDATA")
	}
}

func TestUpdateCredential(t *testing.T) {
	client := NewClient("old_cookie=value")
	if client.credential != nil {
		t.Error("expected nil credential for legacy client")
	}

	cred := &Credential{
		Sessdata: "new_sessdata",
		BiliJCT:  "new_jct",
	}
	client.UpdateCredential(cred)

	if client.credential == nil {
		t.Error("expected non-nil credential after update")
	}
	if !contains(client.cookie, "SESSDATA=new_sessdata") {
		t.Error("cookie should be updated with new SESSDATA")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
