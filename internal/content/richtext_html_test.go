package content

import (
	"strings"
	"testing"
)

func TestHydrateRichTextObjectKeysInjectsSrc(t *testing.T) {
	html := `<p>正文</p><img data-asset-id="ast_1" alt="封面" width="320"><audio data-asset-id="ast_2" controls></audio><video data-asset-id="ast_missing" controls></video>`
	got := hydrateRichTextObjectKeys(html, map[string]string{
		"ast_1": "assets/ast_1/cover.png",
		"ast_2": "assets/ast_2/clip.mp3",
	})
	if !strings.Contains(got, `data-asset-id="ast_1"`) || !strings.Contains(got, `src="assets/ast_1/cover.png"`) {
		t.Fatalf("图片未注入 object_key src: %s", got)
	}
	if !strings.Contains(got, `alt="封面"`) || !strings.Contains(got, `width="320"`) {
		t.Fatalf("应保留图片属性: %s", got)
	}
	if !strings.Contains(got, `data-asset-id="ast_2"`) || !strings.Contains(got, `src="assets/ast_2/clip.mp3"`) {
		t.Fatalf("音频未注入 object_key src: %s", got)
	}
	if strings.Contains(got, `data-asset-id="ast_missing"`) && strings.Contains(got, `ast_missing`) {
		idx := strings.Index(got, `data-asset-id="ast_missing"`)
		fragment := got[idx:]
		if end := strings.Index(fragment, ">"); end >= 0 {
			fragment = fragment[:end]
		}
		if strings.Contains(fragment, `src=`) {
			t.Fatalf("无映射时不应写入 src: %s", got)
		}
	}
}

func TestHydrateRichTextObjectKeysOverridesTemporarySrc(t *testing.T) {
	html := `<img data-asset-id="ast_1" alt="" src="blob:temp">`
	got := hydrateRichTextObjectKeys(html, map[string]string{"ast_1": "assets/ast_1/file.png"})
	if strings.Contains(got, "blob:temp") || !strings.Contains(got, `src="assets/ast_1/file.png"`) {
		t.Fatalf("应覆盖临时 src: %s", got)
	}
}

func TestHydrateRichTextObjectKeysNoopWithoutMapping(t *testing.T) {
	html := `<img data-asset-id="ast_1" alt="x">`
	if got := hydrateRichTextObjectKeys(html, nil); got != html {
		t.Fatalf("无映射时应原样返回: %q", got)
	}
	if got := hydrateRichTextObjectKeys(html, map[string]string{}); got != html {
		t.Fatalf("空映射时应原样返回: %q", got)
	}
}
