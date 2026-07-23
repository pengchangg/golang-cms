package asset

import "testing"

func TestValidRichTextMediaUsesSafePreviewWhitelist(t *testing.T) {
	tests := []struct {
		kind     string
		mimeType string
		want     bool
	}{
		{kind: "image", mimeType: "image/jpeg", want: true},
		{kind: "image", mimeType: "image/avif", want: true},
		{kind: "image", mimeType: "image/svg+xml", want: false},
		{kind: "image", mimeType: "application/pdf", want: false},
		{kind: "audio", mimeType: "audio/mpeg", want: true},
		{kind: "audio", mimeType: "audio/flac", want: false},
		{kind: "video", mimeType: "video/webm", want: true},
		{kind: "video", mimeType: "video/quicktime", want: false},
	}
	for _, test := range tests {
		if got := validRichTextMedia(test.kind, test.mimeType); got != test.want {
			t.Errorf("validRichTextMedia(%q, %q) = %v，期望 %v", test.kind, test.mimeType, got, test.want)
		}
	}
}

func TestArchivedReferencesCannotIncreaseDuringInheritance(t *testing.T) {
	if !canInheritArchivedReferences(2, 2) || !canInheritArchivedReferences(2, 1) {
		t.Fatal("base Revision 已有数量范围内的归档引用应允许继承")
	}
	if canInheritArchivedReferences(1, 2) || canInheritArchivedReferences(0, 1) {
		t.Fatal("归档素材不能在新 Revision 中增加引用数量")
	}
}
