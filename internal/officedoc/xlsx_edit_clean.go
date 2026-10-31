package officedoc

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

// toNumber turns cells that hold a number as text ("93.8%", "1,234", "￥12") into
// real numbers, the way Excel's "convert to number" does, so SUM, comparisons and
// the formulas written for the column treat them like their neighbours. A text
// percentage becomes a fraction with a percent format that shows the same digits.
// Identifier-like text ("007", long digit strings) and anything that is not a
// number are left alone. It returns how many cells were converted.
func toNumber(f *excelize.File, sheet, rng string) (int, error) {
	done := 0
	err := eachCell(f, sheet, rng, maxOpCells, func(cell string) error {
		if t, _ := f.GetCellType(sheet, cell); t != excelize.CellTypeSharedString && t != excelize.CellTypeInlineString {
			return nil
		}
		s, err := f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
		if err != nil {
			return err
		}
		s = strings.TrimSpace(strings.NewReplacer("％", "%", "．", ".").Replace(s))
		if s == "" || codeLike(s) {
			return nil
		}
		v, pct, _, ok := looseNumber(s)
		if !ok {
			return nil
		}
		if pct {
			digits := 0
			if i := strings.Index(s, "."); i >= 0 {
				digits = len([]rune(strings.NewReplacer("%", "", " ", "").Replace(s[i+1:])))
			}
			digits = min(digits, 4)
			code := "0%"
			if digits > 0 {
				code = "0." + strings.Repeat("0", digits) + "%"
			}
			if err := f.SetCellValue(sheet, cell, roundTo(v/100, 10)); err != nil {
				return err
			}
			if err := applyFormat(f, sheet, cell, &xlsxFormat{NumFmt: code}); err != nil {
				return err
			}
		} else if err := f.SetCellValue(sheet, cell, v); err != nil {
			return err
		}
		done++
		return nil
	})
	return done, err
}

// copyFormat gives every cell of rng the style of the single cell src: a new
// column's header and body then look like the columns next to it.
func copyFormat(f *excelize.File, sheet, src, rng string) error {
	if err := cellArg(src); err != nil {
		return fmt.Errorf("copy_format needs cell as the source: %w", err)
	}
	c1, r1, c2, r2, err := parseRange(rng)
	if err != nil {
		return fmt.Errorf("range %q: %w", rng, err)
	}
	if (c2-c1+1)*(r2-r1+1) > maxFormatCells {
		return fmt.Errorf("range %q is too large (max %d cells)", rng, maxFormatCells)
	}
	id, err := f.GetCellStyle(sheet, strings.ReplaceAll(strings.TrimSpace(src), "$", ""))
	if err != nil {
		return err
	}
	tl, _ := excelize.CoordinatesToCellName(c1, r1)
	br, _ := excelize.CoordinatesToCellName(c2, r2)
	return f.SetCellStyle(sheet, tl, br, id)
}
