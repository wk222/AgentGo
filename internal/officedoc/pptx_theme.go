package officedoc

import (
	"fmt"
	"strings"
)

// palette is one deck colour scheme. Colours are RRGGBB without '#'.
type palette struct {
	Primary   string // dark brand colour: title text, cover background, table header
	Secondary string // brand colour: accents, big numbers, chart series 1
	Accent    string // highlight: rules, bullets
	Surface   string // light panel background
	Text      string
	Muted     string
	Line      string
	Soft      string // light text on the Primary background
	Chart     []string
}

var palettes = map[string]palette{
	"blue": {
		Primary: "1F3A5F", Secondary: "2F6DB5", Accent: "F28C28", Surface: "F2F5FA", Text: "1F2937", Muted: "6B7280",
		Line: "D6DEE8", Soft: "C9D8EC", Chart: []string{"2F6DB5", "F28C28", "3FA796", "8E6CC7", "E15759", "8C99A8"},
	},
	"mobile": {
		Primary: "0B4F8A", Secondary: "0085CE", Accent: "FF8F1F", Surface: "EEF6FB", Text: "1B2A38", Muted: "62758A",
		Line: "CFE2F0", Soft: "BFE0F5", Chart: []string{"0085CE", "8CC63F", "FFB400", "00A3A1", "7A5CC8", "E0524D"},
	},
	"green": {
		Primary: "14452F", Secondary: "2E8B57", Accent: "E0A100", Surface: "F1F7F3", Text: "1F2A24", Muted: "5F6F66",
		Line: "D3E3D9", Soft: "CBE5D5", Chart: []string{"2E8B57", "E0A100", "3A7CA5", "8E6CC7", "D9534F", "8A9A91"},
	},
	"red": {
		Primary: "8E1B1B", Secondary: "C0392B", Accent: "C9A227", Surface: "FBF4F2", Text: "2B2220", Muted: "7A6A66",
		Line: "ECD9D4", Soft: "F1CFCB", Chart: []string{"C0392B", "C9A227", "2F6DB5", "3FA796", "8E6CC7", "8C8C8C"},
	},
	"gray": {
		Primary: "262E3A", Secondary: "4B5B70", Accent: "0EA5A4", Surface: "F4F5F7", Text: "1F2328", Muted: "6A727D",
		Line: "DADDE2", Soft: "C8CDD5", Chart: []string{"4B5B70", "0EA5A4", "F59E0B", "6366F1", "EF4444", "9CA3AF"},
	},
}

// themeNames lists the accepted theme values, for error messages.
const themeNames = "blue, mobile, green, red, gray"

const (
	nsA = `xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"`
	nsR = `xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	nsP = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"`
)

const (
	slideW = 13.333
	slideH = 7.5
)

func (d *deck) themeXML(name string) string {
	p := d.pal
	f := escapeXML(d.font)
	var b strings.Builder
	b.WriteString(xmlHeader + `<a:theme ` + nsA + ` name="` + name + `"><a:themeElements>`)
	fmt.Fprintf(&b, `<a:clrScheme name="AgentGo"><a:dk1><a:srgbClr val="%s"/></a:dk1><a:lt1><a:srgbClr val="FFFFFF"/></a:lt1>`+
		`<a:dk2><a:srgbClr val="%s"/></a:dk2><a:lt2><a:srgbClr val="%s"/></a:lt2>`+
		`<a:accent1><a:srgbClr val="%s"/></a:accent1><a:accent2><a:srgbClr val="%s"/></a:accent2>`+
		`<a:accent3><a:srgbClr val="%s"/></a:accent3><a:accent4><a:srgbClr val="%s"/></a:accent4>`+
		`<a:accent5><a:srgbClr val="%s"/></a:accent5><a:accent6><a:srgbClr val="%s"/></a:accent6>`+
		`<a:hlink><a:srgbClr val="%s"/></a:hlink><a:folHlink><a:srgbClr val="%s"/></a:folHlink></a:clrScheme>`,
		p.Text, p.Primary, p.Surface, p.Chart[0], p.Chart[1], p.Chart[2], p.Chart[3], p.Chart[4], p.Chart[5], p.Secondary, p.Muted)
	fmt.Fprintf(&b, `<a:fontScheme name="AgentGo"><a:majorFont><a:latin typeface="%s"/><a:ea typeface="%s"/><a:cs typeface="%s"/></a:majorFont>`+
		`<a:minorFont><a:latin typeface="%s"/><a:ea typeface="%s"/><a:cs typeface="%s"/></a:minorFont></a:fontScheme>`, f, f, f, f, f, f)
	solid := `<a:solidFill><a:schemeClr val="phClr"/></a:solidFill>`
	ln := func(w int) string {
		return fmt.Sprintf(`<a:ln w="%d" cap="flat" cmpd="sng" algn="ctr">%s<a:prstDash val="solid"/><a:miter lim="800000"/></a:ln>`, w, solid)
	}
	b.WriteString(`<a:fmtScheme name="AgentGo"><a:fillStyleLst>` + solid + solid + solid + `</a:fillStyleLst>` +
		`<a:lnStyleLst>` + ln(6350) + ln(12700) + ln(19050) + `</a:lnStyleLst>` +
		`<a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst>` +
		`<a:bgFillStyleLst>` + solid + solid + solid + `</a:bgFillStyleLst></a:fmtScheme>`)
	b.WriteString(`</a:themeElements><a:objectDefaults/><a:extraClrSchemeLst/></a:theme>`)
	return b.String()
}

const spTreeHead = `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
	`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`

func xfrmXML(x, y, w, h float64) string {
	return fmt.Sprintf(`<a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`, emu(x), emu(y), emu(w), emu(h))
}

// placeholderSP is a master/layout placeholder shape.
func placeholderSP(id int, name, phAttrs string, x, y, w, h float64, text string) string {
	body := `<a:p><a:r><a:rPr lang="zh-CN" altLang="en-US"/><a:t>` + escapeXML(text) + `</a:t></a:r></a:p>`
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph %s/></p:nvPr></p:nvSpPr>`+
		`<p:spPr>%s</p:spPr><p:txBody><a:bodyPr/><a:lstStyle/>%s</p:txBody></p:sp>`, id, name, phAttrs, xfrmXML(x, y, w, h), body)
}

// Geometry shared by the master, the layouts and the content slides.
const (
	marginX  = 0.75
	contentW = slideW - 2*marginX
	titleY   = 0.45
	titleH   = 1.0
	bodyY    = 1.85
	bodyH    = 4.85
)

func (d *deck) masterXML(layoutCount int) string {
	var b strings.Builder
	b.WriteString(xmlHeader + `<p:sldMaster ` + nsA + ` ` + nsR + ` ` + nsP + `><p:cSld>` +
		`<p:bg><p:bgRef idx="1001"><a:schemeClr val="bg1"/></p:bgRef></p:bg><p:spTree>` + spTreeHead)
	b.WriteString(placeholderSP(2, "Title Placeholder 1", `type="title"`, marginX, titleY, contentW, titleH, "单击此处编辑母版标题样式"))
	b.WriteString(placeholderSP(3, "Text Placeholder 2", `type="body" idx="1"`, marginX, bodyY, contentW, bodyH, "单击此处编辑母版文本样式"))
	b.WriteString(`</p:spTree></p:cSld>` +
		`<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>` +
		`<p:sldLayoutIdLst>`)
	for i := 1; i <= layoutCount; i++ {
		fmt.Fprintf(&b, `<p:sldLayoutId id="%d" r:id="rId%d"/>`, int64(2147483648)+int64(i), i) // int64: the id exceeds int on 32-bit builds
	}
	title := fmt.Sprintf(`<a:lvl1pPr algn="l" defTabSz="914400" rtl="0" eaLnBrk="1" latinLnBrk="0" hangingPunct="1"><a:lnSpc><a:spcPct val="90000"/></a:lnSpc>` +
		`<a:spcBef><a:spcPct val="0"/></a:spcBef><a:buNone/><a:defRPr sz="3000" b="1" kern="1200"><a:solidFill><a:schemeClr val="tx2"/></a:solidFill>` +
		`<a:latin typeface="+mj-lt"/><a:ea typeface="+mj-ea"/><a:cs typeface="+mj-cs"/></a:defRPr></a:lvl1pPr>`)
	body := ""
	for lvl := 1; lvl <= 3; lvl++ {
		body += fmt.Sprintf(`<a:lvl%dpPr marL="%d" indent="%d" algn="l" defTabSz="914400" rtl="0" eaLnBrk="1" latinLnBrk="0" hangingPunct="1">`+
			`<a:spcBef><a:spcPts val="900"/></a:spcBef><a:buFont typeface="Arial"/><a:buChar char="•"/>`+
			`<a:defRPr sz="%d" kern="1200"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill><a:latin typeface="+mn-lt"/><a:ea typeface="+mn-ea"/><a:cs typeface="+mn-cs"/></a:defRPr></a:lvl%dpPr>`,
			lvl, emu(0.32+0.38*float64(lvl-1)), -emu(0.26), 2400-200*(lvl-1), lvl)
	}
	b.WriteString(`</p:sldLayoutIdLst><p:txStyles><p:titleStyle>` + title + `</p:titleStyle><p:bodyStyle>` + body + `</p:bodyStyle>` +
		`<p:otherStyle><a:defPPr><a:defRPr lang="zh-CN"/></a:defPPr></p:otherStyle></p:txStyles></p:sldMaster>`)
	return b.String()
}

// Layout indexes (1-based, the number in slideLayoutN.xml).
const (
	layoutTitle     = 1 // cover, section, closing
	layoutTitleOnly = 2
	layoutContent   = 3 // title + body placeholder
)

func layoutXML(n int) string {
	var typ, name, shapes string
	switch n {
	case layoutTitle:
		typ, name = "title", "Title Slide"
		shapes = placeholderSP(2, "Title 1", `type="ctrTitle"`, 0.9, 2.75, 11.0, 1.7, "单击此处编辑标题") +
			placeholderSP(3, "Subtitle 2", `type="subTitle" idx="1"`, 0.9, 4.6, 11.0, 0.9, "单击此处编辑副标题")
	case layoutTitleOnly:
		typ, name = "titleOnly", "Title Only"
		shapes = placeholderSP(2, "Title 1", `type="title"`, marginX, titleY, contentW, titleH, "单击此处编辑标题")
	default:
		typ, name = "obj", "Title and Content"
		shapes = placeholderSP(2, "Title 1", `type="title"`, marginX, titleY, contentW, titleH, "单击此处编辑标题") +
			placeholderSP(3, "Content Placeholder 2", `idx="1"`, marginX, bodyY, contentW, bodyH, "单击此处编辑文本")
	}
	return xmlHeader + `<p:sldLayout ` + nsA + ` ` + nsR + ` ` + nsP + ` type="` + typ + `" preserve="1"><p:cSld name="` + name + `"><p:spTree>` +
		spTreeHead + shapes + `</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`
}

const layoutCount = 3

func notesMasterXML() string {
	return xmlHeader + `<p:notesMaster ` + nsA + ` ` + nsR + ` ` + nsP + `><p:cSld><p:bg><p:bgRef idx="1001"><a:schemeClr val="bg1"/></p:bgRef></p:bg><p:spTree>` + spTreeHead +
		`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Slide Image Placeholder 1"/><p:cNvSpPr><a:spLocks noGrp="1" noRot="1" noChangeAspect="1"/></p:cNvSpPr><p:nvPr><p:ph type="sldImg" idx="2"/></p:nvPr></p:nvSpPr>` +
		`<p:spPr><a:xfrm><a:off x="685800" y="1143000"/><a:ext cx="5486400" cy="3086100"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/>` +
		`<a:ln w="12700"><a:solidFill><a:prstClr val="black"/></a:solidFill></a:ln></p:spPr></p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Notes Placeholder 2"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="body" sz="quarter" idx="3"/></p:nvPr></p:nvSpPr>` +
		`<p:spPr><a:xfrm><a:off x="685800" y="4400550"/><a:ext cx="5486400" cy="3600450"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr>` +
		`<p:txBody><a:bodyPr vert="horz" lIns="91440" tIns="45720" rIns="91440" bIns="45720" rtlCol="0"/><a:lstStyle/><a:p><a:pPr lvl="0"/><a:r><a:rPr lang="zh-CN" altLang="en-US"/><a:t>单击此处编辑母版文本样式</a:t></a:r></a:p></p:txBody></p:sp>` +
		`</p:spTree></p:cSld>` +
		`<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>` +
		`<p:notesStyle><a:lvl1pPr marL="0" algn="l" defTabSz="914400" rtl="0" eaLnBrk="1" latinLnBrk="0" hangingPunct="1"><a:defRPr sz="1200" kern="1200"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill>` +
		`<a:latin typeface="+mn-lt"/><a:ea typeface="+mn-ea"/><a:cs typeface="+mn-cs"/></a:defRPr></a:lvl1pPr></p:notesStyle></p:notesMaster>`
}

func notesSlideXML(text string) string {
	var ps strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		ps.WriteString(`<a:p><a:r><a:rPr lang="zh-CN" altLang="en-US" dirty="0"/><a:t>` + escapeXML(strings.TrimSpace(line)) + `</a:t></a:r></a:p>`)
	}
	return xmlHeader + `<p:notes ` + nsA + ` ` + nsR + ` ` + nsP + `><p:cSld><p:spTree>` + spTreeHead +
		`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Slide Image Placeholder 1"/><p:cNvSpPr><a:spLocks noGrp="1" noRot="1" noChangeAspect="1"/></p:cNvSpPr><p:nvPr><p:ph type="sldImg"/></p:nvPr></p:nvSpPr><p:spPr/></p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Notes Placeholder 2"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="body" idx="1"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		`<p:txBody><a:bodyPr/><a:lstStyle/>` + ps.String() + `</p:txBody></p:sp></p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:notes>`
}
