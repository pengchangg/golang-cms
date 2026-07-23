package content

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

const maxRichTextRunes = 524288

var (
	richTextAssetIDPattern = regexp.MustCompile(`^ast_[A-Za-z0-9._-]{1,128}$`)
	richTextDimension      = regexp.MustCompile(`^[1-9][0-9]{0,4}$`)
	richTextHTMLPolicy     = newRichTextHTMLPolicy()
)

func newRichTextHTMLPolicy() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowElements(
		"p", "br", "h1", "h2", "h3", "h4", "h5", "h6",
		"ul", "ol", "li", "blockquote", "pre", "code",
		"strong", "b", "em", "i", "u", "s", "strike", "span",
		"a", "table", "thead", "tbody", "tr", "th", "td",
		"img", "audio", "video",
	)
	policy.AllowAttrs("href").OnElements("a")
	policy.AllowAttrs("colspan", "rowspan").OnElements("th", "td")
	policy.AllowAttrs("data-asset-id", "alt", "width", "height").OnElements("img")
	policy.AllowAttrs("data-asset-id", "controls").OnElements("audio", "video")
	policy.AllowStandardURLs()
	policy.RequireParseableURLs(true)
	policy.AllowURLSchemes("http", "https", "mailto")
	policy.AllowRelativeURLs(false)
	return policy
}

func sanitizeRichTextHTML(value string) string {
	return strings.TrimSpace(richTextHTMLPolicy.Sanitize(value))
}

func validateRichTextHTML(value string, path string, failures *validationErrors) string {
	if utf8.RuneCountInString(value) > maxRichTextRunes {
		failures.add(path, "too_long", fmt.Sprintf("富文本最多 %d 个字符", maxRichTextRunes))
		return value
	}
	cleaned := sanitizeRichTextHTML(value)
	validateRichTextMediaElements(cleaned, path, failures)
	return cleaned
}

func validateRichTextMediaElements(value, path string, failures *validationErrors) {
	tokenizer := html.NewTokenizer(strings.NewReader(value))
	index := 0
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			switch token.Data {
			case "img", "audio", "video":
				mediaPath := fmt.Sprintf("%s/media/%d", path, index)
				index++
				validateRichTextMediaToken(token, mediaPath, failures)
			}
		}
	}
}

func validateRichTextMediaToken(token html.Token, path string, failures *validationErrors) {
	kind := token.Data
	attrs := map[string]string{}
	for _, attr := range token.Attr {
		attrs[attr.Key] = attr.Val
	}
	for key := range attrs {
		allowed := key == "data-asset-id" || (kind == "img" && (key == "alt" || key == "width" || key == "height")) || ((kind == "audio" || kind == "video") && key == "controls")
		if !allowed {
			failures.add(path+"/@"+key, "unknown_property", "富文本媒体包含未允许属性")
		}
	}
	assetID := attrs["data-asset-id"]
	if assetID == "" || !richTextAssetIDPattern.MatchString(assetID) {
		failures.add(path+"/@data-asset-id", "invalid_type", "媒体 data-asset-id 必须是非空素材 ID")
	}
	if kind == "img" {
		alt, ok := attrs["alt"]
		if !ok {
			failures.add(path+"/@alt", "invalid_type", "image alt 必须存在")
		} else if utf8.RuneCountInString(alt) > 1000 {
			failures.add(path+"/@alt", "too_long", "image alt 最多 1000 个字符")
		}
		for _, key := range []string{"width", "height"} {
			if value, exists := attrs[key]; exists && !richTextDimension.MatchString(value) {
				failures.add(path+"/@"+key, "invalid_value", key+" 必须是 1 至 99999 的整数")
			}
		}
	}
	if kind == "audio" || kind == "video" {
		if _, ok := attrs["alt"]; ok {
			failures.add(path+"/@alt", "unknown_property", kind+" 不允许 alt")
		}
	}
}

func richTextMediaKind(tag string) string {
	if tag == "img" {
		return "image"
	}
	return tag
}

func appendRichTextHTMLMediaReferences(result *[]MediaReference, value string, revision Revision, fieldID, pointer string) {
	tokenizer := html.NewTokenizer(strings.NewReader(value))
	position := 0
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			switch token.Data {
			case "img", "audio", "video":
				assetID := ""
				for _, attr := range token.Attr {
					if attr.Key == "data-asset-id" {
						assetID = attr.Val
						break
					}
				}
				if assetID != "" {
					*result = append(*result, MediaReference{
						RevisionID:  revision.ID,
						EntryID:     revision.EntryID,
						ModelID:     revision.ModelID,
						FieldID:     fieldID,
						AssetID:     assetID,
						JSONPointer: fmt.Sprintf("%s/media/%d", pointer, position),
						Position:    position,
						Kind:        richTextMediaKind(token.Data),
					})
				}
				position++
			}
		}
	}
}

// hydrateRichTextObjectKeys injects src=object_key for Content API responses. Persistence stays without src.
func hydrateRichTextObjectKeys(value string, objectKeys map[string]string) string {
	if value == "" || len(objectKeys) == 0 {
		return value
	}
	doc, err := html.Parse(strings.NewReader(value))
	if err != nil {
		return value
	}
	body := richTextHTMLBody(doc)
	if body == nil {
		return value
	}
	changed := false
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "img", "audio", "video":
				assetID := htmlAttr(node, "data-asset-id")
				if key := objectKeys[assetID]; key != "" {
					setHTMLAttr(node, "src", key)
					changed = true
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(body)
	if !changed {
		return value
	}
	var buf strings.Builder
	for child := body.FirstChild; child != nil; child = child.NextSibling {
		_ = html.Render(&buf, child)
	}
	return buf.String()
}

func richTextHTMLBody(doc *html.Node) *html.Node {
	var body *html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node == nil || body != nil {
			return
		}
		if node.Type == html.ElementNode && node.Data == "body" {
			body = node
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return body
}

func htmlAttr(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func setHTMLAttr(node *html.Node, key, value string) {
	for i := range node.Attr {
		if node.Attr[i].Key == key {
			node.Attr[i].Val = value
			return
		}
	}
	node.Attr = append(node.Attr, html.Attribute{Key: key, Val: value})
}
