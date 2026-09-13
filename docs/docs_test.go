package docs

import (
	"strings"
	"testing"
)

// TestUserGuideNoTrailingContent guards against an editing accident where
// markup is appended after the document ends. A stray table row placed after
// </html> once rendered as narrow columns bleeding into the page's right
// gutter (the digest option row escaped the notify table). Browsers relocate
// such orphaned content into the body with default styling, so it is easy to
// miss without a rendered check — this asserts the source has nothing but
// whitespace after the closing tag.
func TestUserGuideNoTrailingContent(t *testing.T) {
	data, err := FS.ReadFile("user-guide.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)

	if n := strings.Count(html, "</html>"); n != 1 {
		t.Fatalf("expected exactly one </html>, got %d", n)
	}

	idx := strings.LastIndex(html, "</html>")
	trailing := strings.TrimSpace(html[idx+len("</html>"):])
	if trailing != "" {
		t.Errorf("content after </html> (should be empty):\n%q", trailing)
	}

	// A table row must never appear after the body closes — that is the exact
	// shape of the orphaned-row bug this test exists to catch.
	body := strings.LastIndex(html, "</body>")
	if body >= 0 && strings.Contains(html[body:], "<tr") {
		t.Error("found <tr after </body> — a table row escaped the document body")
	}
}
