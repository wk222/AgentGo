package textenc

import (
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func gbk(t *testing.T, s string) []byte {
	t.Helper()
	b, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatalf("encode %q as GBK: %v", s, err)
	}
	return b
}

func utf16Bytes(s string, order binary.AppendByteOrder, bom bool) []byte {
	var out []byte
	if bom {
		if order == binary.LittleEndian {
			out = append(out, 0xFF, 0xFE)
		} else {
			out = append(out, 0xFE, 0xFF)
		}
	}
	for _, u := range utf16.Encode([]rune(s)) {
		out = order.AppendUint16(out, u)
	}
	return out
}

var officeSentences = []string{
	"合同金额：人民币壹拾万元整",
	"采购部 2026年度预算申报表",
	"项目负责人：张三；联系电话：13800000000",
	"第三条　付款方式：验收合格后三十日内支付。",
}

func TestDecode(t *testing.T) {
	long := strings.Repeat(strings.Join(officeSentences, "\r\n")+"\r\n", 30)

	cases := []struct {
		name    string
		in      []byte
		want    string
		wantEnc string
	}{
		{"empty", nil, "", ""},
		{"ascii", []byte("id,amount\n1,2.5\n"), "id,amount\n1,2.5\n", ""},
		{"utf-8", []byte(long), long, ""},
		{"utf-8 with BOM", append([]byte{0xEF, 0xBB, 0xBF}, long...), long, "utf-8-bom"},
		{"utf-16le with BOM", utf16Bytes(long, binary.LittleEndian, true), long, "utf-16le"},
		{"utf-16be with BOM", utf16Bytes(long, binary.BigEndian, true), long, "utf-16be"},
		{"gbk", gbk(t, long), long, "gb18030"},
		{"gbk, one line", gbk(t, officeSentences[2]), officeSentences[2], "gb18030"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, enc := Decode(c.in)
			if got != c.want {
				t.Fatalf("text mismatch\n got: %q\nwant: %q", got, c.want)
			}
			if enc != c.wantEnc {
				t.Fatalf("encoding = %q, want %q", enc, c.wantEnc)
			}
		})
	}
}

// A CSV exported from Excel on a Chinese Windows machine: pages of numbers and a
// header of a few Chinese characters. Few bytes are invalid UTF-8 in absolute
// terms, so a rule based on the share of bad bytes would call it UTF-8 and lose
// exactly the header.
func TestDecodeGBKCSVWithFewChineseCharacters(t *testing.T) {
	for _, header := range []string{"编号,金额\n", "项目,100\n", "姓名\n"} {
		csv := header + strings.Repeat("1001,123.45,2026-01-02\n", 400)
		got, enc := Decode(gbk(t, csv))
		if got != csv || enc != "gb18030" {
			t.Fatalf("header %q: encoding %q, header after decoding = %q", header, enc, firstLine(got))
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// One damaged byte in an otherwise good UTF-8 document must not turn the whole
// document into noise by sending it down the GBK path.
func TestDecodeKeepsUTF8WithAStrayByte(t *testing.T) {
	good := strings.Repeat("中文合同金额", 20)
	in := []byte(good + "\xff" + good)
	got, enc := Decode(in)
	if enc != "utf-8-lossy" {
		t.Fatalf("encoding = %q, want utf-8-lossy", enc)
	}
	if want := good + "\uFFFD" + good; got != want {
		t.Fatalf("damaged UTF-8 was not kept readable: got %q", got)
	}
}

func TestDecodeGB18030FourByteCharacter(t *testing.T) {
	// U+20000 is outside GBK and needs a four-byte GB18030 sequence.
	want := "合同" + "\U00020000" + "金额，" + strings.Repeat("甲乙丙丁", 10)
	b, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(want))
	if err != nil {
		t.Fatal(err)
	}
	if got, enc := Decode(b); got != want || enc != "gb18030" {
		t.Fatalf("got %q (%s), want %q", got, enc, want)
	}
}

func TestDecodeOddUTF16ReportsTruncation(t *testing.T) {
	in := append(utf16Bytes("合同", binary.LittleEndian, true), 0x41) // dangling byte
	got, enc := Decode(in)
	if enc != "utf-16le" || got != "合同\uFFFD" {
		t.Fatalf("got %q (%s)", got, enc)
	}
}

func TestDecodeBinaryIsNotRelabelledAsChinese(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0x0D, 'I', 'H', 'D', 'R', 0xB0, 0xA1}
	_, enc := Decode(png)
	if enc != "unknown" {
		t.Fatalf("binary data was reported as %q", enc)
	}
}

func TestCharCountCountsCharactersNotBytes(t *testing.T) {
	if got := CharCount("合同金额"); got != 4 {
		t.Fatalf("charCount = %d, want 4 (it is %d bytes)", got, len("合同金额"))
	}
}
