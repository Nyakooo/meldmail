package mail

import (
	"strings"
	"testing"
)

func TestMailHTMLKeepsSafeLayout(t *testing.T) {
	source := `<ul style="list-style:none;padding:0;display:flex"><li style="width:25%;text-align:center;color:#333">-</li></ul><table><tr><td colspan="2" style="padding:12px;background-color:#eee">Report</td></tr></table><span style="background-image:url(https://tracker.invalid/pixel);position:fixed" onclick="alert(1)">Text</span><img src="https://tracker.invalid/pixel"><script>alert(1)</script><style>.secret{display:none}</style>`
	result := mailHTML.Sanitize(source)
	for _, keep := range []string{"list-style: none", "display: flex", "width: 25%", `colspan="2"`, "padding: 12px", "background-color: #eee"} {
		if !strings.Contains(result, keep) {
			t.Errorf("safe layout lost %q: %s", keep, result)
		}
	}
	for _, blocked := range []string{"tracker.invalid", "onclick", "position:", "<script", "<style", "<img", "alert(1)"} {
		if strings.Contains(result, blocked) {
			t.Errorf("unsafe markup retained %q", blocked)
		}
	}
}

func TestMailStylesKeepClassesWithoutNetworkAccess(t *testing.T) {
	source := `<style>@import url(https://tracker.invalid/css);.detail-mark{list-style:none;padding:0}.detail-mark li{display:inline-block;width:25%;background-image:url(https://tracker.invalid/pixel)}.bad{position:fixed;behavior:url(x)}</style><ul class="detail-mark"><li>-</li></ul>`
	styles := safeMailStyles(source)
	for _, keep := range []string{".detail-mark{", "list-style: none", "display: inline-block", "width: 25%"} {
		if !strings.Contains(styles, keep) {
			t.Errorf("missing %q: %s", keep, styles)
		}
	}
	for _, bad := range []string{"@import", "tracker.invalid", "background-image", "position", "behavior"} {
		if strings.Contains(styles, bad) {
			t.Errorf("unsafe stylesheet %q", bad)
		}
	}
	if !strings.Contains(mailHTML.Sanitize(source), `class="detail-mark"`) {
		t.Fatal("layout class removed")
	}
}
