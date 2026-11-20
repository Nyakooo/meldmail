package imapclient

import (
	"encoding/base64"
	"github.com/emersion/go-imap/v2"
	"strings"
	"testing"
)

func TestSnippetEncodingsAndMultipart(t *testing.T) {
	cases := []struct {
		name, data string
		structure  imap.BodyStructure
		want       string
	}{
		{"base64", base64.StdEncoding.EncodeToString([]byte("中文验证码 123456")), &imap.BodyStructureSinglePart{Type: "text", Subtype: "plain", Encoding: "base64", Params: map[string]string{"charset": "utf-8"}}, "中文验证码 123456"},
		{"quoted printable", "hello=20world", &imap.BodyStructureSinglePart{Type: "text", Subtype: "plain", Encoding: "quoted-printable"}, "hello world"},
		{"html", "<script>secret()</script><style>hidden</style><p>Hello &amp; 中文</p>", &imap.BodyStructureSinglePart{Type: "text", Subtype: "html"}, "Hello & 中文"},
		{"nested multipart", "--outer\r\nContent-Type: multipart/alternative; boundary=inner\r\n\r\n--inner\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nNested 中文\r\n--inner--\r\n--outer--", &imap.BodyStructureMultiPart{Subtype: "mixed"}, "Nested 中文"},
		{"binary", strings.Repeat("binary", 100), &imap.BodyStructureSinglePart{Type: "application", Subtype: "octet-stream"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := snippet([]byte(c.data), c.structure); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}
