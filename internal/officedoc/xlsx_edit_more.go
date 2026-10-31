package officedoc

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/xuri/excelize/v2"
)

// xlsxRule is a conditional-format rule: the cells that meet it get the
// colours of the op's format, and keep them live when the data changes.
type xlsxRule struct {
	Type     string `json:"type" jsonschema:"required,description=cell (compare each cell with a value) or formula (a formula that is true for the rows to colour)."`
	Criteria string `json:"criteria,omitempty" jsonschema:"description=cell rules: > \\, < \\, >= \\, <= \\, = \\, != or between."`
	Value    any    `json:"value,omitempty" jsonschema:"description=cell rules: the value to compare with (between: the lower bound). Compare with the STORED value: a 0.0% cell showing 96% holds 0.96."`
	Value2   any    `json:"value2,omitempty" jsonschema:"description=between: the upper bound."`
	Formula  string `json:"formula,omitempty" jsonschema:"description=formula rules: written for the top-left cell of range with relative references\\, for example =AND($B4=DATE(2026\\,9\\,1)\\,$C4<0.96) for range C4:C147."`
}

// cellRefRe finds A1-style references. The caller checks what surrounds a match.
var cellRefRe = regexp.MustCompile(`(\$?)([A-Z]{1,3})(\$?)([0-9]{1,7})`)

// shiftFormula rewrites a formula written for one cell so it fits the cell dr
// rows and dc columns away, the way Excel's fill does: relative references move,
// $-anchored ones stay. Text in quotes and quoted sheet names are left alone.
func shiftFormula(formula string, dr, dc int) (string, error) {
	var out strings.Builder
	rs := []rune(formula)
	for i := 0; i < len(rs); {
		ch := rs[i]
		if ch == '"' || ch == '\'' { // a string literal or a quoted sheet name
			j := i + 1
			for j < len(rs) {
				if rs[j] == ch {
					if j+1 < len(rs) && rs[j+1] == ch {
						j += 2
						continue
					}
					break
				}
				j++
			}
			end := min(j+1, len(rs))
			out.WriteString(string(rs[i:end]))
			i = end
			continue
		}
		// copy a run that cannot contain a reference as is, then test the next token
		j := i
		for j < len(rs) && rs[j] != '"' && rs[j] != '\'' {
			j++
		}
		seg := string(rs[i:j])
		shifted, err := shiftSegment(seg, dr, dc)
		if err != nil {
			return "", err
		}
		out.WriteString(shifted)
		i = j
	}
	return out.String(), nil
}

func isNameRune(r byte) bool {
	return r == '_' || r == '.' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r >= 0x80
}

func shiftSegment(seg string, dr, dc int) (string, error) {
	idx := cellRefRe.FindAllStringSubmatchIndex(seg, -1)
	if len(idx) == 0 {
		return seg, nil
	}
	var out strings.Builder
	last := 0
	for _, m := range idx {
		s, e := m[0], m[1]
		before := s > 0 && isNameRune(seg[s-1]) && !(seg[s-1] == '$')
		after := e < len(seg) && (isNameRune(seg[e]) || seg[e] == '(' || seg[e] == '!')
		if before || after {
			continue // part of a name, a function such as LOG10( or a sheet name such as Q1!
		}
		colAbs, col, rowAbs, row := seg[m[2]:m[3]], seg[m[4]:m[5]], seg[m[6]:m[7]], seg[m[8]:m[9]]
		c, _ := excelize.ColumnNameToNumber(col)
		r := atoi(row)
		if colAbs == "" {
			c += dc
		}
		if rowAbs == "" {
			r += dr
		}
		if c < 1 || c > 16384 || r < 1 || r > 1048576 {
			return "", fmt.Errorf("the reference %s%s%s%s moves outside the sheet when filled (use $ to anchor it)", colAbs, col, rowAbs, row)
		}
		out.WriteString(seg[last:s])
		out.WriteString(colAbs + colName(c) + rowAbs + fmt.Sprint(r))
		last = e
	}
	out.WriteString(seg[last:])
	return out.String(), nil
}

// fillFormula writes formula (written for the top-left cell of rng) into every
// cell of rng, adjusting relative references like a fill-down in Excel.
func fillFormula(f *excelize.File, sheet, rng, formula string) error {
	formula = strings.TrimSpace(formula)
	formula = strings.TrimSpace(strings.TrimPrefix(formula, "="))
	if formula == "" {
		return errors.New("formula is required")
	}
	c1, r1, _, _, err := parseRange(rng)
	if err != nil {
		return fmt.Errorf("range %q: %w", rng, err)
	}
	return eachCell(f, sheet, rng, maxOpCells, func(cell string) error {
		c, r, _ := excelize.CellNameToCoordinates(cell)
		shifted, err := shiftFormula(formula, r-r1, c-c1)
		if err != nil {
			return fmt.Errorf("%s: %w", cell, err)
		}
		return f.SetCellFormula(sheet, cell, shifted)
	})
}

var cfCriteria = map[string]string{
	">": ">", "<": "<", ">=": ">=", "<=": "<=", "=": "==", "==": "==", "!=": "!=", "<>": "!=", "between": "between",
}

// conditionalFormat colours the cells of rng that meet rule, with the fill and
// font colour of fm.
func conditionalFormat(f *excelize.File, sheet, rng string, rule *xlsxRule, fm *xlsxFormat) error {
	if rule == nil {
		return errors.New("rule is required")
	}
	if fm == nil || (fm.Fill == "" && fm.FontColor == "" && fm.Bold == nil) {
		return errors.New("format needs a fill, font_color or bold: that is how the matching cells are marked")
	}
	c1, r1, c2, r2, err := parseRange(rng)
	if err != nil {
		return fmt.Errorf("range %q: %w", rng, err)
	}
	tl, _ := excelize.CoordinatesToCellName(c1, r1)
	br, _ := excelize.CoordinatesToCellName(c2, r2)
	style := &excelize.Style{}
	if fm.Fill != "" {
		fill, err := normalizeColor(fm.Fill)
		if err != nil {
			return err
		}
		style.Fill = excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{fill}}
	}
	if fm.FontColor != "" || fm.Bold != nil {
		style.Font = &excelize.Font{}
		if fm.FontColor != "" {
			if style.Font.Color, err = normalizeColor(fm.FontColor); err != nil {
				return err
			}
		}
		if fm.Bold != nil {
			style.Font.Bold = *fm.Bold
		}
	}
	id, err := f.NewConditionalStyle(style)
	if err != nil {
		return err
	}
	opt := excelize.ConditionalFormatOptions{Format: &id}
	switch strings.ToLower(strings.TrimSpace(rule.Type)) {
	case "cell":
		crit, ok := cfCriteria[strings.TrimSpace(rule.Criteria)]
		if !ok {
			return fmt.Errorf("criteria %q must be one of >, <, >=, <=, =, != or between", rule.Criteria)
		}
		if rule.Value == nil {
			return errors.New("value is required for a cell rule")
		}
		opt.Type, opt.Criteria, opt.Value = "cell", crit, valueText(rule.Value)
		if crit == "between" {
			if rule.Value2 == nil {
				return errors.New("between needs value and value2")
			}
			opt.Value, opt.MinValue, opt.MaxValue = "", valueText(rule.Value), valueText(rule.Value2)
		}
	case "formula":
		form := strings.TrimSpace(rule.Formula)
		if form == "" {
			return errors.New("formula is required for a formula rule")
		}
		// The file format stores a rule's formula without the leading "=": with it
		// WPS ignores the rule and Excel may report the file as damaged.
		form = strings.TrimSpace(strings.TrimPrefix(form, "="))
		if form == "" {
			return errors.New("formula is required for a formula rule")
		}
		opt.Type, opt.Criteria = "formula", form
	default:
		return fmt.Errorf("rule type %q must be cell or formula", rule.Type)
	}
	return f.SetConditionalFormat(sheet, tl+":"+br, []excelize.ConditionalFormatOptions{opt})
}
