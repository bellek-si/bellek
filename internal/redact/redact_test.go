package redact

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The banners are built from two pieces because a whole one in the source
// trips the secret scanner on commit.
const (
	beginRSA = "-----BEGIN RSA PRIVATE " + "KEY-----"
	endRSA   = "-----END RSA PRIVATE " + "KEY-----"
	beginPGP = "-----BEGIN PGP PRIVATE " + "KEY BLOCK-----"
	endPGP   = "-----END PGP PRIVATE " + "KEY BLOCK-----"
)

func TestText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "fix the login bug in internal/auth.go", "fix the login bug in internal/auth.go"},
		{"private block", "keep this <private>not this\nnor this</private> and this", "keep this  and this"},
		{"anthropic key", "key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123", "key [redacted:anthropic]"},
		{"openai key", "sk-proj-abcdefghijklmnopqrstuvwxyz0123", "[redacted:openai]"},
		{"github token", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", "[redacted:github]"},
		{"aws key", "AKIAIOSFODNN7EXAMPLE used", "[redacted:aws] used"},
		{"jwt", strings.Join([]string{"eyJhbGciOiJIUzI1NiJ9", "eyJzdWIiOiIxMjM0NTY3ODkwIn0", "dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"}, "."), "[redacted:jwt]"},
		{"bearer header", "Authorization: Bearer abcdefghijklmnopqrstuvwxyz", "Authorization: Bearer [redacted:bearer]"},
		{"url credentials", "mysql://app:hunter2secret@db.example.com:3306/app", "mysql://app:[redacted:url]@db.example.com:3306/app"},
		{"env assignment", "BELLEK_TOKEN=change-me-to-sixteen-plus-chars", "BELLEK_TOKEN=[redacted:env]"},
		{"yaml assignment", "password: \"correct-horse-battery\"", "password: [redacted:env]"},
		{"short value untouched", "token=abc", "token=abc"},
		{"pem", beginRSA + "\nMIIE\n" + endRSA, "[redacted:pem]"},
		{"blob", "data " + strings.Repeat("QUJD", 60) + " end", "data [blob] end"},
		{"slack", "xoxb-1234567890-abcdefghijkl", "[redacted:slack]"},
		{"the word token in a sentence", "the token expired while I was reading", "the token expired while I was reading"},
		{"bellek task key in json", `{"taskKey":"auth-refresh","projectKey":"bellek-web"}`, `{"taskKey":"auth-refresh","projectKey":"bellek-web"}`},
		{"bellek keys in yaml", "parentKey: phase-work-2026\nrelatedKey: use-rotation-tokens", "parentKey: phase-work-2026\nrelatedKey: use-rotation-tokens"},
		{"api key still redacted", `{"apiKey":"correct-horse-battery","taskKey":"auth-refresh"}`, `{"apiKey":[redacted:env],"taskKey":"auth-refresh"}`},
		{"API_KEY still redacted", "API_KEY=change-me-to-sixteen-plus-chars", "API_KEY=[redacted:env]"},
		{"a bare key is still redacted", "key: correct-horse-battery", "key: [redacted:env]"},
		{"a name that only starts like a bellek key", "taskKeySecret=correct-horse-battery", "taskKeySecret=[redacted:env]"},
		{"a path", "cmd/bellek/hook.go:42", "cmd/bellek/hook.go:42"},
		{"an email address", "write to someone@example.com about it", "write to someone@example.com about it"},
		{"a private block hides the secret inside it", "<private>sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123</private>done", "done"},

		{"sql server spelling", "Password=Sup3rS3cretPwd", "Password=[redacted:env]"},
		{"azure storage spelling", "AccountKey=abcdefghijklmnopqrs", "AccountKey=[redacted:env]"},
		{"camel case token", "accessToken=abcdefghijklmnop", "accessToken=[redacted:env]"},
		{"camel case api key", "ApiKey: abcdefghijklmnop", "ApiKey: [redacted:env]"},
		{"capitalised secret", "Secret: abcdefghijklmnop", "Secret: [redacted:env]"},
		{"credential keyword", "dbCredential=abcdefghijklmnop", "dbCredential=[redacted:env]"},
		{"a word ending in key is not an assignment", "Monkey: see the diff", "Monkey: see the diff"},

		{"pem cut before its end line", beginRSA + "\nMIIEowIBAAKCAQEA\nvQIDAQABAoIBAA", "[redacted:pem]"},
		{"an unclosed private marker drops the rest", "keep this <private>not this\nnor this", "keep this [redacted:private]"},
		{"a quoted marker the user never wrote drops the rest visibly", "wrap it in a <private> tag and the hook drops it", "wrap it in a [redacted:private]"},

		{"password only redis url", "redis://:justapassword@localhost:6379", "redis://:[redacted:url]@localhost:6379"},
		{"password only mongodb url", "mongodb://:justapassword@localhost:27017", "mongodb://:[redacted:url]@localhost:27017"},

		{"slack app token", "xapp-" + "1-A012345678-1234567890123-abcdef", "[redacted:slack]"},
		{"sendgrid key", "SG." + strings.Repeat("a", 22) + "." + strings.Repeat("b", 43), "[redacted:sendgrid]"},
		{"hugging face token", "hf_" + strings.Repeat("a", 34), "[redacted:huggingface]"},
		{"docker hub token", "dckr_pat_" + strings.Repeat("a", 28), "[redacted:docker]"},

		{"shell spelling", "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY", "AWS_SECRET_ACCESS_KEY=[redacted:env]"},
		{"json spelling", `{"AWS_SECRET_ACCESS_KEY":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}`, `{"AWS_SECRET_ACCESS_KEY":[redacted:env]}`},
		{"json with a space after the colon", `{"apiKey": "sk_live_notmatching_abcdefghijkl"}`, `{"apiKey": [redacted:env]}`},
		{"json password", `{"password": "abcdefghijklmn"}`, `{"password": [redacted:env]}`},
		{"double quoted value with spaces", `PASSWORD="correct horse battery staple"`, "PASSWORD=[redacted:env]"},
		{"single quoted value with spaces", `DB_PASSWORD='my secret value here'`, "DB_PASSWORD=[redacted:env]"},
		{"escaped quote inside the value", `{"password": "abc\"defghijkl"}`, `{"password": [redacted:env]}`},
		{"quoted value cut before its closing quote", `"password": "abcdefghijklmn`, `"password": [redacted:env]`},
		{"a short quoted value untouched", `{"token": "abc"}`, `{"token": "abc"}`},

		{"pem with headers cut before its end line", beginRSA + "\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,ABCDEF\n\nMIIEowIBAAKCAQEAAAA", "[redacted:pem]"},
		{"text before a cut pem stays", "see below\n" + beginRSA + "\nMIIEowIBAAKCAQEA", "see below\n[redacted:pem]"},
		{"text after a whole pem stays", beginRSA + "\nMIIE\n" + endRSA + "\nafter", "[redacted:pem]\nafter"},
		{"pgp private key block", beginPGP + "\nlQOYBF\n" + endPGP, "[redacted:pem]"},
		{"pgp private key block cut before its end line", beginPGP + "\n\nlQOYBFabcdefgh", "[redacted:pem]"},

		{"basic auth header", "Authorization: Basic dXNlcm5hbWU6c3VwZXJzZWNyZXRwYXNzd29yZA==", "Authorization: Basic [redacted:auth]"},
		{"proxy basic auth header", "Proxy-Authorization: Basic dXNlcm5hbWU6c3VwZXJzZWNyZXRwYXNzd29yZA==", "Proxy-Authorization: Basic [redacted:auth]"},
		{"basic auth header in json", `{"Authorization": "Basic dXNlcm5hbWU6c3VwZXJzZWNyZXRwYXNzd29yZA=="}`, `{"Authorization": "Basic [redacted:auth]"}`},
		{"token auth scheme", "Authorization: Token abcdefghijklmnopqrstuvwxyz", "Authorization: Token [redacted:auth]"},
		{"curl user flag", "curl -u admin:changeme https://api.internal/x", "curl -u admin:[redacted:user] https://api.internal/x"},
		{"curl long user flag", "curl --user=admin:changeme https://api.internal/x", "curl --user=admin:[redacted:user] https://api.internal/x"},
		{"a -u flag with no password untouched", "git push -u origin main", "git push -u origin main"},
		{"set cookie header", "Set-Cookie: session=abcdef0123456789; Path=/; HttpOnly", "Set-Cookie: [redacted:cookie]"},
		{"cookie header", "Cookie: theme=dark; sid=abcdef0123456789", "Cookie: [redacted:cookie]"},
		{"the word cookie in a sentence untouched", "the session cookie: HttpOnly and SameSite", "the session cookie: HttpOnly and SameSite"},
		{"signature keyword", "X-Amz-Signature=abcdef0123456789abcdef", "X-Amz-Signature=[redacted:env]"},
		{"npmrc auth", "_auth=changemechangeme", "_auth=[redacted:env]"},
		{"docker config auth", `{"auth": "changemechangeme"}`, `{"auth": [redacted:env]}`},
		{"personal access token name", "GITHUB_PAT=abcdefghijklmnopqrstuvwx", "GITHUB_PAT=[redacted:env]"},
		{"PATH untouched", "PATH=/usr/local/bin:/usr/bin", "PATH=/usr/local/bin:/usr/bin"},
		{"author untouched", `"author": "Jane Doe <jane@example.com>"`, `"author": "Jane Doe <jane@example.com>"`},
		{"a session id untouched", `"sessionId": "01a0cd24-1257-74a7-a193-3ae7f0bc3288"`, `"sessionId": "01a0cd24-1257-74a7-a193-3ae7f0bc3288"`},

		{"base64 wrapped at 76 columns", strings.Repeat(strings.Repeat("QUJD", 19)+"\n", 4) + "QUJDQUJD", "[blob]"},
		{"base64 wrapped at 64 columns between text", "before\n" + strings.Repeat("QUJD", 16) + "\n" + strings.Repeat("QUJD", 16) + "\n" + strings.Repeat("QUJD", 16) + "\nafter it", "before\n[blob]\nafter it"},
		{"indented wrapped base64", "tls.key: |\n    " + strings.Repeat("QUJD", 16) + "\n    " + strings.Repeat("QUJD", 16) + "\n    QUJD==\nnext: value", "tls.key: |\n[blob]\nnext: value"},
		{"one long line alone is not a blob", strings.Repeat("QUJD", 16), strings.Repeat("QUJD", 16)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Text(tt.in, 0))
		})
	}
}

func TestTextCaps(t *testing.T) {
	require.Len(t, Text(strings.Repeat("a", 100), 10), 10)
	require.Equal(t, "abc", Text("abc", 10))
	require.Equal(t, "éé", Text(strings.Repeat("é", 10), 5), "the cap never splits a rune")
	require.Equal(t, "a", Text("a€b", 2))
	require.Equal(t, "", Text("€", 2))
}

func TestTextRedactsACRLFPem(t *testing.T) {
	require.Equal(t, "[redacted:pem]", Text(beginRSA+"\r\nMIIEowIBAAKCAQEA\r\nvQIDAQABAoIBAA", 0))
}

func TestTextSparesEveryBellekKeyName(t *testing.T) {
	const v = "auth-refresh-2026"
	for _, name := range []string{"taskKey", "projectKey", "parentKey", "relatedKey", "noteKey", "focusKey", "turnTaskKey", "nodeKey"} {
		for _, form := range []string{name + "=" + v, name + ": " + v, `"` + name + `": "` + v + `"`, strings.ToUpper(name) + "=" + v} {
			t.Run(form, func(t *testing.T) {
				require.Equal(t, form, Text(form, 0))
			})
		}
	}
}

func TestTextRedactsNamesCloseToABellekKey(t *testing.T) {
	tests := []struct{ in, want string }{
		{"TASK_KEY=correct-horse-battery", "TASK_KEY=[redacted:env]"},
		{"task_key: correct-horse-battery", "task_key: [redacted:env]"},
		{"mytaskKey=correct-horse-battery", "mytaskKey=[redacted:env]"},
		{"taskKeys=correct-horse-battery", "taskKeys=[redacted:env]"},
		{"taskKey_auth=correct-horse-battery", "taskKey_auth=[redacted:env]"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			require.Equal(t, tt.want, Text(tt.in, 0))
		})
	}
}

func TestTextSparesAKeyNameAndRedactsTheSecretNextToIt(t *testing.T) {
	require.Equal(t, "taskKey = correct-horse-battery apiKey = [redacted:env] tail", Text("taskKey = correct-horse-battery apiKey = correct-horse-battery tail", 0))
	require.Equal(t, "apiKey=[redacted:env]\ntaskKey=correct-horse-battery\nsecret=[redacted:env]", Text("apiKey=correct-horse-battery\ntaskKey=correct-horse-battery\nsecret=correct-horse-battery", 0))
}

func TestTextStillRedactsAKnownSecretShapeUnderABellekKeyName(t *testing.T) {
	require.Equal(t, "taskKey=[redacted:anthropic]", Text("taskKey=sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123", 0))
}
