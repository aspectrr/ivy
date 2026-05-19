package github

import (
	"testing"
)

// Test key generated with: openssl genrsa 2048 (ephemeral, for tests only)
const testRSAKeyPEM = `-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQCSVvRd3jJgTRxN
HF7IKOtSpLHTV/by698/v+UwRYbj2sUll433kqwwmTE3Q+2kCQBZMhIkLScGaboO
oTydGOzcBlMOp3TjEsHhF/h8q4NUuTJPjvJWjal6LNoDjL2v1ax4vcO0suxDs2bV
a9XWD0+TxcRg7IdO15yfzrqFWB9XV0HHF2FJXr4o3tsolK21f8HGUOnjH1CDj9YH
3LyjvGNWXYfTg1ciTUh23JoUpY6u1pnkC2wunicSR1YUpYi+ILSz4KyOEPIltL3J
+d4LbGrVK4XTXXWxyOXmk0V7J9Zc6DDkuDmkGYnbOgYRs2TNVNnCjlB+i9zHs9Ui
+X3m2swVAgMBAAECggEAKQLaeAYByzBBCrE1NNYW4PHL7iU8TWbiCW0fb1BE1c1l
K7xV6nh97h64hrrwOeTV5qlcISxQQAFYRapVINev5ZeWJkiyvsJueEUt+85bP16p
ZVdzveL0iItSS+Vg8Yqpy6qu0pDEGtMHsi8G3fcrf4fQmbMf1m4hdD3M0vrXybN7
am/JWqxYAjPv4v5+8wn6yTnPZZgG33NWfS1QIZt/isCrWH8vV+jsba1w7xRJPAft
JQeayru4IoviA26sfnlEexbq7nku7sWb9kMc4DUbczvVSnFPbLOZqyHVHTUgBWYQ
d4UHlKuY74xQJu4OYP35kFVKpLbdpFX1fU8D+5wsvwKBgQDIqo58w4sdojRjqvV7
/M4XfH2hVp1X7SGyo9rIgkuMzawibMUQobAWrPPgSZdU5bKWphbs1Uo5OzQtGlCT
RduMlKP/mbeLToq3iyXdK0+a/CWi+7B6DNUUSDjqQ6waCNmqI74E89HMB97uLKBI
ojzlRls5CyvNjEsyxmlOU1J9/wKBgQC6sV+TPAUc5d21yhi88BtweAz+ZA1weOfd
AiYT5bvWrUOxQH0ktRamL+JnM2cJdrh2G/Ec53UwHt8UDUkXYULZvcYmYzT0PUpW
XYFsXvy8xnXE+4fonHwG4Yk/qrkN9yDLaAW9C53BB4flRijpkjnaPBzcMsjSmqYH
R2snhKTd6wKBgDv9AOu7aXNKcm75RLn0MYhD5yq8Qf1vHovRAC7BBOTq93KzIZZ/
P60Ht0Btv5fZszHmJSRX/wBs+oQhQcVFNQUpyn027u/uYvnL113u/LVQe8/lfjR+
cZTGon0mDeUakDeUx9GjMizUjYiWPrR4C8xe5BaBiG7CahibyA9qSVbxAoGAWJCg
mIZWnpjljsHq7maxfa9V6rCoN30D8bJ9Qd8wNu1HOaUwOOO3dOsuamrWLIUniNBE
l8OtskBS735F+FNplUYT5E4X5u3UgBgnt7NwDlXPtLzmgpEJvXHs3EkvNNLRue0F
G+OQ2OurqjaYXgXCcCcoQcXNwyseLEHTMZIZbDUCgYEAr7mucxUIRpGOMgdsUV1I
tgP4xKPAQ67ICbJNRxHhDBZpsHHBiFGdPRttBLU1Wec9h+qSod6N69uCCMdjyfn8
wcOY/7mVQholV/t82vpbagLNM30VN92+1PEgLOmEA4+af7mkjVbdti0occv8S1if
FoutBOdKzTx7sAt1hCRLQuA=
-----END PRIVATE KEY-----`

func TestParsePrivateKey(t *testing.T) {
	tests := []struct {
		name    string
		pemStr  string
		wantErr bool
	}{
		{
			name:    "empty string",
			pemStr:  "",
			wantErr: true,
		},
		{
			name:    "invalid PEM",
			pemStr:  "not a PEM block",
			wantErr: true,
		},
		{
			name:    "valid PKCS8 RSA key",
			pemStr:  testRSAKeyPEM,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parsePrivateKey(tt.pemStr)
			if (err != nil) != tt.wantErr {
				t.Errorf("parsePrivateKey() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "missing app ID",
			cfg:     Config{AppID: 0, PrivateKey: testRSAKeyPEM},
			wantErr: true,
		},
		{
			name:    "missing private key",
			cfg:     Config{AppID: 12345, PrivateKey: ""},
			wantErr: true,
		},
		{
			name:    "invalid private key",
			cfg:     Config{AppID: 12345, PrivateKey: "not a key"},
			wantErr: true,
		},
		{
			name:    "valid config with default base URL",
			cfg:     Config{AppID: 12345, PrivateKey: testRSAKeyPEM},
			wantErr: false,
		},
		{
			name:    "valid config with GHES base URL",
			cfg:     Config{AppID: 12345, PrivateKey: testRSAKeyPEM, BaseURL: "https://github.example.com/api/v3"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewClient() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if client == nil {
					t.Error("NewClient() returned nil client")
				}
				if client.AppID() != tt.cfg.AppID {
					t.Errorf("AppID() = %d, want %d", client.AppID(), tt.cfg.AppID)
				}
			}
		})
	}
}

func TestNewClient_BaseURL(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected string
	}{
		{
			name:     "default base URL",
			baseURL:  "",
			expected: defaultBaseURL,
		},
		{
			name:     "GHES base URL",
			baseURL:  "https://github.example.com/api/v3",
			expected: "https://github.example.com/api/v3",
		},
		{
			name:     "trailing slash trimmed",
			baseURL:  "https://github.example.com/api/v3/",
			expected: "https://github.example.com/api/v3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(Config{
				AppID:      12345,
				PrivateKey: testRSAKeyPEM,
				BaseURL:    tt.baseURL,
			})
			if err != nil {
				t.Fatalf("NewClient() error: %v", err)
			}
			if client.BaseURL() != tt.expected {
				t.Errorf("BaseURL() = %q, want %q", client.BaseURL(), tt.expected)
			}
		})
	}
}

func TestNewClient_GeneratesJWT(t *testing.T) {
	client, err := NewClient(Config{
		AppID:      12345,
		PrivateKey: testRSAKeyPEM,
	})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}

	jwtStr, err := client.generateJWT()
	if err != nil {
		t.Fatalf("generateJWT() error: %v", err)
	}
	if jwtStr == "" {
		t.Error("generateJWT() returned empty string")
	}
	// JWT should have 3 parts separated by dots.
	parts := len(splitJWT(jwtStr))
	if parts != 3 {
		t.Errorf("JWT should have 3 parts, got %d", parts)
	}
}

// splitJWT splits a JWT string by dots.
func splitJWT(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}
