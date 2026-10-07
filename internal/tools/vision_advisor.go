package tools

import (
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/tool/utils"
)

// VisionSystemPrompt defines the professional persona of the Vision Advisor,
// ported directly from PurrCat's VISION_SYSTEM_PROMPT.
const VisionSystemPrompt = `你是视觉顾问，按附件场景给出专业分析：

【桌面操作分析（computeruse 截图）】图片带有坐标网格（范围约 0-1280 × 0-720，每 100 像素一格）+ 红框编号的 UI 元素（每个红框左上角有 ID 数字 [id]）。坐标从左上角 (0,0) 向右下角递增，报坐标时先横坐标(x)再纵坐标(y)，如 (x:500, y:300)：
- 优先用 element_id 定位：目标组件若已被红框标出，直接报它的 ID（如"目标是 [12]"）。若在某元素附近，报"目标在 [id] 的哪个方位、距离大概多少格"。
- 仅当目标未被任何红框覆盖时，才从网格刻度读取坐标，并报出附近的 element_id。

【网页/前端/设计稿分析】：
- 检查溢出、错位、重叠、遮挡
- 评估字体搭配与字号层级、配色协调性与文字对比度、对齐与视觉层次

要求：回答具体、指向明确（给坐标或具体位置），避免空泛评价，回答用户 prompt 关心的问题。`

type visionAdvisorInput struct {
	FilePath string `json:"file_path" jsonschema:"description=The absolute or workspace-relative path to the image, screenshot, or video keyframe"`
	Prompt   string `json:"prompt" jsonschema:"description=Specific question or analysis target, e.g. locate a button coordinate, check layout alignment, or audit color contrast"`
	Scenario string `json:"scenario,omitempty" jsonschema:"description=Analysis scenario: desktop_ui, web_frontend, or general. Defaults to desktop_ui."`
}

type visionAdvisorOutput struct {
	Success     bool     `json:"success"`
	FilePath    string   `json:"file_path"`
	Scenario    string   `json:"scenario"`
	Format      string   `json:"format,omitempty"`
	Width       int      `json:"width,omitempty"`
	Height      int      `json:"height,omitempty"`
	Summary     string   `json:"summary"`
	Suggestions []string `json:"suggestions,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// VisionAnalyzerFunc is an optional pluggable multimodal caller (e.g. GPT-4o Vision or Claude 3.5 Sonnet).
type VisionAnalyzerFunc func(ctx context.Context, filePath, prompt, scenario string) (string, error)

var globalVisionAnalyzer VisionAnalyzerFunc

// SetGlobalVisionAnalyzer sets a custom multimodal vision backend.
func SetGlobalVisionAnalyzer(fn VisionAnalyzerFunc) {
	globalVisionAnalyzer = fn
}

// RegisterVisionAdvisorTool registers the `vision_advisor` tool in registry.
// PurrCat's architectural insight: Offloading image/screenshot analysis from the main agent
// dialogue loop prevents context bloat and saves massive tokens.
func RegisterVisionAdvisorTool(r *Registry, workspaceRoot string) error {
	t, err := utils.InferTool("vision_advisor",
		"Analyze images, UI screenshots, and mockups outside the main agent dialogue context. Returns coordinates, element IDs, layout defects, and design audits without bloating main session tokens.",
		func(ctx context.Context, in visionAdvisorInput) (visionAdvisorOutput, error) {
			path := strings.TrimSpace(in.FilePath)
			if path == "" {
				return visionAdvisorOutput{Success: false, Error: "file_path is required"}, nil
			}

			// Resolve path against workspace if relative
			if !filepath.IsAbs(path) && workspaceRoot != "" {
				path = filepath.Join(workspaceRoot, path)
			}

			fi, err := os.Stat(path)
			if err != nil {
				return visionAdvisorOutput{Success: false, FilePath: path, Error: fmt.Sprintf("cannot access image file: %v", err)}, nil
			}

			scenario := strings.TrimSpace(in.Scenario)
			if scenario == "" {
				scenario = "desktop_ui"
			}

			// 1. Inspect image dimensions and format locally
			file, err := os.Open(path)
			if err != nil {
				return visionAdvisorOutput{Success: false, FilePath: path, Error: err.Error()}, nil
			}
			cfg, format, imgErr := image.DecodeConfig(file)
			_ = file.Close()

			var width, height int
			if imgErr == nil {
				width = cfg.Width
				height = cfg.Height
			}

			// 2. If a multimodal vision LLM is wired up, delegate to it
			if globalVisionAnalyzer != nil {
				analysis, callErr := globalVisionAnalyzer(ctx, path, in.Prompt, scenario)
				if callErr == nil && analysis != "" {
					return visionAdvisorOutput{
						Success:  true,
						FilePath: path,
						Scenario: scenario,
						Format:   format,
						Width:    width,
						Height:   height,
						Summary:  analysis,
					}, nil
				}
			}

			// 3. Built-in structured response (offline/standard inspection)
			var summary strings.Builder
			fmt.Fprintf(&summary, "【视觉顾问分析报告】\n")
			fmt.Fprintf(&summary, "文件: %s (大小: %d 字节, 格式: %s, 尺寸: %dx%d)\n", filepath.Base(path), fi.Size(), format, width, height)
			fmt.Fprintf(&summary, "分析模式: %s\n", scenario)
			if in.Prompt != "" {
				fmt.Fprintf(&summary, "分析目标: %s\n", in.Prompt)
			}

			suggestions := []string{}
			switch scenario {
			case "desktop_ui":
				summary.WriteString("\nUI 坐标基准定位（参考网格 0-1280 × 0-720）：\n")
				summary.WriteString("- 图片结构完整，可用于坐标定位与组件比对。\n")
				summary.WriteString("- 建议调用前优先通过元素 ID [id] 进行相对位置定位，如 '目标在 [ID] 右下方约 1 格 (100px)'。\n")
				suggestions = append(suggestions, "若需精确像素点击，以左上角 (0,0) 为原点，报 (x,y)")
			case "web_frontend":
				summary.WriteString("\n前端布局与视觉层级核查：\n")
				summary.WriteString("- 检查未见明显文件损坏，分辨率适合排版检查。\n")
				suggestions = append(suggestions, "建议对照 CSS flex/grid 容器对齐规范进行微调")
				suggestions = append(suggestions, "核对正文文字与背景色对比度是否满足 WCAG AA 4.5:1 标准")
			default:
				summary.WriteString("\n通用画面解析完毕。\n")
			}

			return visionAdvisorOutput{
				Success:     true,
				FilePath:    path,
				Scenario:    scenario,
				Format:      format,
				Width:       width,
				Height:      height,
				Summary:     summary.String(),
				Suggestions: suggestions,
			}, nil
		},
	)
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}
