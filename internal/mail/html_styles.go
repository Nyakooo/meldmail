package mail

import (
	"html"
	"io"
	"regexp"
	"strings"

	"github.com/aymerick/douceur/css"
	"github.com/aymerick/douceur/parser"
	htmlparser "golang.org/x/net/html"
)

// Only ordinary selectors inside the isolated reader are retained. At-rules
// (including imports/fonts), URL properties and executable CSS are discarded.
var mailSelector = regexp.MustCompile(`^[a-zA-Z0-9_.#*, >+~:-]+$`)

func safeMailStyles(source string) string {
	const maxCSS = 128 * 1024
	var output strings.Builder
	tokens := htmlparser.NewTokenizer(strings.NewReader(source))
	inStyle := false
	used := 0
	for {
		switch tokens.Next() {
		case htmlparser.ErrorToken:
			if tokens.Err() != io.EOF {
				return ""
			}
			if output.Len() == 0 {
				return ""
			}
			return "<style>" + output.String() + "</style>"
		case htmlparser.StartTagToken:
			name, _ := tokens.TagName()
			inStyle = string(name) == "style"
		case htmlparser.EndTagToken:
			inStyle = false
		case htmlparser.TextToken:
			if !inStyle {
				continue
			}
			raw := tokens.Text()
			used += len(raw)
			if used > maxCSS {
				continue
			}
			sheet, err := parser.Parse(string(raw))
			if err != nil {
				continue
			}
			for _, rule := range sheet.Rules {
				if rule.Kind != css.QualifiedRule || !mailSelector.MatchString(rule.Prelude) {
					continue
				}
				var declarations strings.Builder
				for _, decl := range rule.Declarations {
					declarations.WriteString(decl.String())
				}
				// Reuse the same per-property validators as inline styles.
				cleaned := mailHTML.Sanitize(`<span style="` + html.EscapeString(declarations.String()) + `"></span>`)
				tag := htmlparser.NewTokenizer(strings.NewReader(cleaned))
				if tag.Next() != htmlparser.StartTagToken {
					continue
				}
				for _, attr := range tag.Token().Attr {
					if attr.Key == "style" && !strings.ContainsAny(attr.Val, "<>") {
						output.WriteString(rule.Prelude)
						output.WriteByte('{')
						output.WriteString(attr.Val)
						output.WriteByte('}')
					}
				}
			}
		}
	}
}
