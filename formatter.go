package smartlimbo

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"go.minekube.com/common/minecraft/color"
	c "go.minekube.com/common/minecraft/component"
)

var (
	rainbowTagRegex      = regexp.MustCompile(`(?i)<rainbow(?::(!)?(?:reversed)?:?([0-9.-]+)?)?>(.*?)</rainbow(?::[^>]*)?>`)
	mineDownRainbowRegex = regexp.MustCompile(`(?i)&rainbow(?::([0-9.-]+))?&(.*?)(?:&r|&reset|&rainbow&|$)`)
	gradientTagRegex     = regexp.MustCompile(`(?i)<gradient:([^>]+)>(.*?)</gradient>`)
	tagHexRegex          = regexp.MustCompile(`(?i)<#([0-9a-f]{6})>`)
	tagShortHex          = regexp.MustCompile(`(?i)<#([0-9a-f]{3})>`)
	tagColorRegex        = regexp.MustCompile(`(?i)<color:#([0-9a-f]{6})>`)

	legacyColorMap = map[rune]color.Color{
		'0': color.Black,
		'1': color.DarkBlue,
		'2': color.DarkGreen,
		'3': color.DarkAqua,
		'4': color.DarkRed,
		'5': color.DarkPurple,
		'6': color.Gold,
		'7': color.Gray,
		'8': color.DarkGray,
		'9': color.Blue,
		'a': color.Green,
		'b': color.Aqua,
		'c': color.Red,
		'd': color.LightPurple,
		'e': color.Yellow,
		'f': color.White,
	}

	namedColorHexMap = map[string]string{
		"black":        "#000000",
		"dark_blue":    "#0000aa",
		"dark_green":   "#00aa00",
		"dark_aqua":    "#00aaaa",
		"dark_red":     "#aa0000",
		"dark_purple":  "#aa00aa",
		"gold":         "#ffaa00",
		"gray":         "#aaaaaa",
		"grey":         "#aaaaaa",
		"dark_gray":    "#555555",
		"dark_grey":    "#555555",
		"blue":         "#5555ff",
		"green":        "#55ff55",
		"aqua":         "#55ffff",
		"red":          "#ff5555",
		"light_purple": "#ff55ff",
		"pink":         "#ff55ff",
		"yellow":       "#ffff55",
		"white":        "#ffffff",
	}
)

// FormatText parses MiniMessage, RGB, and Legacy color tags into a Minecraft Component.
func FormatText(input string) c.Component {
	if input == "" {
		return &c.Text{Content: ""}
	}

	// 1. Process <rainbow>
	input = rainbowTagRegex.ReplaceAllStringFunc(input, func(m string) string {
		sub := rainbowTagRegex.FindStringSubmatch(m)
		if len(sub) < 4 {
			return m
		}
		isReversed := sub[1] == "!" || strings.Contains(strings.ToLower(m), "reversed")
		phase := 0.0
		if sub[2] != "" {
			if parsed, err := strconv.ParseFloat(sub[2], 64); err == nil {
				phase = parsed
			}
		}
		return applyRainbow(sub[3], phase, isReversed)
	})

	// 2. Process &rainbow&
	input = mineDownRainbowRegex.ReplaceAllStringFunc(input, func(m string) string {
		sub := mineDownRainbowRegex.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		phase := 0.0
		if sub[1] != "" {
			if parsed, err := strconv.ParseFloat(sub[1], 64); err == nil {
				phase = parsed
			}
		}
		return applyRainbow(sub[2], phase, false)
	})

	// 3. Process <gradient:...>
	input = gradientTagRegex.ReplaceAllStringFunc(input, func(m string) string {
		sub := gradientTagRegex.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		return applyMultiGradient(sub[2], sub[1])
	})

	// 4. Short hex
	input = tagShortHex.ReplaceAllStringFunc(input, func(m string) string {
		sub := tagShortHex.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		s := sub[1]
		return fmt.Sprintf("<#%c%c%c%c%c%c>", s[0], s[0], s[1], s[1], s[2], s[2])
	})

	input = tagHexRegex.ReplaceAllString(input, "&#$1")
	input = tagColorRegex.ReplaceAllString(input, "&#$1")

	for name, hex := range namedColorHexMap {
		input = strings.ReplaceAll(input, fmt.Sprintf("<%s>", name), "&#"+strings.TrimPrefix(hex, "#"))
		input = strings.ReplaceAll(input, fmt.Sprintf("</%s>", name), "&r")
	}

	// Styles
	input = strings.ReplaceAll(input, "<b>", "&l")
	input = strings.ReplaceAll(input, "<bold>", "&l")
	input = strings.ReplaceAll(input, "</b>", "&r")
	input = strings.ReplaceAll(input, "</bold>", "&r")
	input = strings.ReplaceAll(input, "<i>", "&o")
	input = strings.ReplaceAll(input, "<italic>", "&o")
	input = strings.ReplaceAll(input, "</i>", "&r")
	input = strings.ReplaceAll(input, "</italic>", "&r")
	input = strings.ReplaceAll(input, "<u>", "&n")
	input = strings.ReplaceAll(input, "<underlined>", "&n")
	input = strings.ReplaceAll(input, "</u>", "&r")
	input = strings.ReplaceAll(input, "</underlined>", "&r")
	input = strings.ReplaceAll(input, "<s>", "&m")
	input = strings.ReplaceAll(input, "<strikethrough>", "&m")
	input = strings.ReplaceAll(input, "</s>", "&r")
	input = strings.ReplaceAll(input, "</strikethrough>", "&r")
	input = strings.ReplaceAll(input, "<reset>", "&r")
	input = strings.ReplaceAll(input, "</reset>", "&r")
	input = strings.ReplaceAll(input, "<r>", "&r")
	input = strings.ReplaceAll(input, "</r>", "&r")

	return parseLegacyTokens(input)
}

func parseLegacyTokens(s string) c.Component {
	root := &c.Text{Content: ""}
	var (
		curColor      color.Color
		bold          c.State
		italic        c.State
		underlined    c.State
		strikethrough c.State
		obfuscated    c.State
		buf           strings.Builder
	)

	flush := func() {
		if buf.Len() > 0 {
			child := &c.Text{
				Content: buf.String(),
				S: c.Style{
					Color:         curColor,
					Bold:          bold,
					Italic:        italic,
					Underlined:    underlined,
					Strikethrough: strikethrough,
					Obfuscated:    obfuscated,
				},
			}
			root.Extra = append(root.Extra, child)
			buf.Reset()
		}
	}

	resetFormat := func() {
		bold = c.NotSet
		italic = c.NotSet
		underlined = c.NotSet
		strikethrough = c.NotSet
		obfuscated = c.NotSet
	}

	runes := []rune(s)
	n := len(runes)
	i := 0

	for i < n {
		r := runes[i]
		if (r == '&' || r == '§') && i+1 < n {
			next := runes[i+1]

			if next == '#' && i+7 < n {
				hexStr := string(runes[i+2 : i+8])
				if col, err := color.Hex("#" + hexStr); err == nil {
					flush()
					curColor = col
					resetFormat()
					i += 8
					continue
				}
			}

			lower := next
			if lower >= 'A' && lower <= 'Z' {
				lower = lower + ('a' - 'A')
			}

			if col, ok := legacyColorMap[lower]; ok {
				flush()
				curColor = col
				resetFormat()
				i += 2
				continue
			}

			switch lower {
			case 'l':
				flush()
				bold = c.True
				i += 2
				continue
			case 'o':
				flush()
				italic = c.True
				i += 2
				continue
			case 'n':
				flush()
				underlined = c.True
				i += 2
				continue
			case 'm':
				flush()
				strikethrough = c.True
				i += 2
				continue
			case 'k':
				flush()
				obfuscated = c.True
				i += 2
				continue
			case 'r':
				flush()
				curColor = nil
				resetFormat()
				i += 2
				continue
			}
		}

		buf.WriteRune(r)
		i++
	}

	flush()
	return root
}

func applyRainbow(text string, phase float64, reversed bool) string {
	runes := []rune(text)
	length := len(runes)
	if length == 0 {
		return ""
	}

	fPhase := phase / 10.0
	center := 128.0
	width := 127.0
	frequency := (math.Pi * 2.0) / float64(length)
	if reversed {
		frequency = -frequency
	}

	var b strings.Builder
	for i, r := range runes {
		idx := float64(i)
		rVal := math.Sin(frequency*idx+2.0+fPhase)*width + center
		gVal := math.Sin(frequency*idx+0.0+fPhase)*width + center
		bVal := math.Sin(frequency*idx+4.0+fPhase)*width + center

		curR := clampColor(int(math.Round(rVal)))
		curG := clampColor(int(math.Round(gVal)))
		curB := clampColor(int(math.Round(bVal)))

		b.WriteString(fmt.Sprintf("&#%02x%02x%02x%c", curR, curG, curB, r))
	}
	return b.String()
}

func applyMultiGradient(text string, colorDefs string) string {
	parts := strings.Split(colorDefs, ":")
	if len(parts) < 2 {
		return text
	}

	var colors [][3]int
	for _, p := range parts {
		clean := strings.TrimSpace(p)
		if clean == "" {
			continue
		}
		if _, err := strconv.ParseFloat(clean, 64); err == nil && len(colors) >= 2 {
			continue
		}
		if hex, ok := parseColorToHex(clean); ok {
			r, g, b := hexToRGB(hex)
			colors = append(colors, [3]int{r, g, b})
		}
	}

	if len(colors) == 0 {
		return text
	}
	if len(colors) == 1 {
		return fmt.Sprintf("&#%02x%02x%02x%s", colors[0][0], colors[0][1], colors[0][2], text)
	}

	runes := []rune(text)
	length := len(runes)
	if length == 0 {
		return ""
	}
	if length == 1 {
		return fmt.Sprintf("&#%02x%02x%02x%c", colors[0][0], colors[0][1], colors[0][2], runes[0])
	}

	numSegments := len(colors) - 1
	var b strings.Builder

	for i, r := range runes {
		progress := float64(i) / float64(length-1)
		scaledProgress := progress * float64(numSegments)
		segIdx := int(math.Floor(scaledProgress))
		if segIdx >= numSegments {
			segIdx = numSegments - 1
		}
		segProgress := scaledProgress - float64(segIdx)

		c1 := colors[segIdx]
		c2 := colors[segIdx+1]

		curR := clampColor(int(math.Round(float64(c1[0]) + segProgress*float64(c2[0]-c1[0]))))
		curG := clampColor(int(math.Round(float64(c1[1]) + segProgress*float64(c2[1]-c1[1]))))
		curB := clampColor(int(math.Round(float64(c1[2]) + segProgress*float64(c2[2]-c1[2]))))

		b.WriteString(fmt.Sprintf("&#%02x%02x%02x%c", curR, curG, curB, r))
	}

	return b.String()
}

func parseColorToHex(s string) (string, bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = fmt.Sprintf("%c%c%c%c%c%c", s[0], s[0], s[1], s[1], s[2], s[2])
	}
	if len(s) == 6 {
		if _, err := strconv.ParseInt(s, 16, 32); err == nil {
			return "#" + s, true
		}
	}
	if hex, ok := namedColorHexMap[strings.ToLower(s)]; ok {
		return hex, true
	}
	return "", false
}

func hexToRGB(hex string) (int, int, int) {
	val, err := strconv.ParseInt(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil {
		return 255, 255, 255
	}
	return int((val >> 16) & 0xFF), int((val >> 8) & 0xFF), int(val & 0xFF)
}

func clampColor(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}
