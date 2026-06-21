package modules

import "testing"

func TestJoinsEmoji(t *testing.T) {
	zwjSize := len(string(rune(0x200D))) // 3

	// Teacher emoji: U+1F9D1 (🧑) ZWJ U+1F3EB (🏫) — built from runes, no literal ZWJ in source.
	teacher := []byte(string([]rune{0x1F9D1, 0x200D, 0x1F3EB}))
	zwjAt := len(string(rune(0x1F9D1))) // 4
	if !joinsEmoji(teacher, zwjAt, zwjSize) {
		t.Error("ZWJ between two emoji must be recognized as a legitimate emoji sequence")
	}

	// ZWJ between letters ("a<ZWJ>b") is a hidden-text vector — must NOT be treated as emoji.
	letters := []byte("a" + string(rune(0x200D)) + "b")
	if joinsEmoji(letters, 1, zwjSize) {
		t.Error("ZWJ between letters must NOT be classified as an emoji sequence")
	}
}

func TestIsEmojiRune(t *testing.T) {
	for _, r := range []rune{0x1F9D1, 0x1F3EB, 0x2764, 0xFE0F, 0x1F1FA} {
		if !isEmojiRune(r) {
			t.Errorf("U+%04X should be an emoji rune", r)
		}
	}
	for _, r := range []rune{'a', 'Z', '0', 0x200D, ' '} {
		if isEmojiRune(r) {
			t.Errorf("U+%04X should NOT be an emoji rune", r)
		}
	}
}
