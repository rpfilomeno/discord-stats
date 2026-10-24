package render

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rpfilomeno/discord-stats/internal/stats"
)

//go:embed template.html
var tplSrc string

var tpl = template.Must(template.New("page").Funcs(template.FuncMap{
	"num":   num,
	"short": short,
	"area":  areaSVG,
	"heat":  heatSVG,
	"bars":  bars,
	"cloud": cloud,
	"day":   dayLabel,
	"pct":   func(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) },
	"i":     func(f float64) int64 { return int64(f) },
}).Parse(tplSrc))

type page struct {
	stats.Stats
	HasData   bool
	SinceTxt  string
	UntilTxt  string
	GenTxt    string
	DayCount  int
	MembersN  int
	TableRows []row
}

type row struct {
	Alias  int
	Name   string
	Avatar string
	Count  int64
	Share  float64
	Avg    int64
}

func Render(s stats.Stats, loc *time.Location) ([]byte, error) {
	p := page{Stats: s, HasData: s.Total > 0, DayCount: len(s.Daily)}
	p.GenTxt = s.Generated.In(loc).Format("2 Jan 2006, 15:04 MST")
	p.MembersN = int(s.Active)
	if p.HasData {
		p.SinceTxt = s.Since.In(loc).Format("2 Jan 2006")
		p.UntilTxt = s.Until.In(loc).Format("2 Jan 2006")
		for _, m := range s.MembersTop {
			var avg int64
			if m.Count > 0 {
				avg = m.Chars / m.Count
			}
			p.TableRows = append(p.TableRows, row{
				Alias: m.Alias, Name: m.Name, Avatar: avatarURL(m.UserID, m.Avatar),
				Count: m.Count, Share: m.Share, Avg: avg,
			})
		}
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, p); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// avatarURL rebuilds the public CDN link from the hash we stored. Animated
// hashes still get a .png: first frame, ~2 KB, and no autoplaying animation in a
// table of fifty rows.
func avatarURL(id int64, hash string) string {
	if hash != "" {
		return fmt.Sprintf("https://cdn.discordapp.com/avatars/%d/%s.png?size=64", id, hash)
	}
	return fmt.Sprintf("https://cdn.discordapp.com/embed/avatars/%d.png", (id>>22)%6)
}

func num(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func short(n int64) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64) + "M"
	case n >= 1_000:
		return strconv.FormatFloat(float64(n)/1_000, 'f', 1, 64) + "k"
	}
	return strconv.FormatInt(n, 10)
}

func dayLabel(day int64) string {
	return time.Unix(day*86400, 0).UTC().Format("2 Jan")
}

const (
	chartW, chartH   = 1000.0, 260.0
	padL, padR, padT = 46.0, 10.0, 14.0
	padB             = 26.0
)

func areaSVG(points []stats.Point) template.HTML {
	if len(points) == 0 {
		return ""
	}
	innerW, innerH := chartW-padL-padR, chartH-padT-padB
	base := padT + innerH
	n := float64(len(points))
	step := innerW / math.Max(n-1, 1)

	var daily int64
	for _, p := range points {
		if p.Count > daily {
			daily = p.Count
		}
	}
	max := niceMax(daily)
	if max == 0 {
		max = 1
	}
	x := func(i int) float64 { return padL + float64(i)*step }
	y := func(v float64) float64 { return base - v/float64(max)*innerH }

	// 7-day moving average: the daily series is too jagged to read as a line.
	ma := make([]float64, len(points))
	window := 0
	var sum float64
	for i, p := range points {
		sum += float64(p.Count)
		window++
		if window > 7 {
			sum -= float64(points[i-7].Count)
			window = 7
		}
		ma[i] = sum / float64(window)
	}

	var line, holes, bars strings.Builder
	var pts []string
	for i, p := range points {
		pts = append(pts, fmt.Sprintf("%.1f,%.1f", x(i), y(ma[i])))
		bw := math.Max(step*0.72, 1)
		fmt.Fprintf(&bars, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="#58a6ff" opacity="0.22"/>`,
			x(i)-bw/2, y(float64(p.Count)), bw, base-y(float64(p.Count)))
		if p.Up >= 0 && p.Up < 720 { // collector was down more than half the day
			fmt.Fprintf(&holes, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="#f85149" opacity="0.12"/>`,
				x(i)-step/2, padT, math.Max(step, 1), innerH)
		}
	}
	line.WriteString("M" + pts[0])
	for _, pt := range pts[1:] {
		line.WriteString(" L" + pt)
	}
	area := line.String() + fmt.Sprintf(" L%.1f,%.1f L%.1f,%.1f Z",
		x(len(points)-1), base, x(0), base)

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="Messages per day, with a 7-day average line">`, chartW, chartH)
	b.WriteString(holes.String())
	b.WriteString(bars.String())
	// grid + y labels on a rounded scale
	for i := 0; i <= 4; i++ {
		v := float64(max) * float64(4-i) / 4
		yy := y(v)
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="currentColor" stroke-opacity="0.10" vector-effect="non-scaling-stroke"/>`, padL, yy, chartW-padR, yy)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" fill="currentColor" opacity="0.55" font-size="10" text-anchor="end">%s</text>`, padL-6, yy+3, short(int64(v)))
	}
	fmt.Fprintf(&b, `<path d="%s" fill="#58a6ff" fill-opacity="0.16"/>`, area)
	fmt.Fprintf(&b, `<path d="%s" fill="none" stroke="#58a6ff" stroke-width="1.8" vector-effect="non-scaling-stroke"/>`, line.String())
	// hover targets
	for i, p := range points {
		tip := dayLabel(p.Day) + ": " + num(p.Count) + " messages"
		if p.Up >= 0 {
			tip += fmt.Sprintf(" · collector up %d%%", p.Up*100/1440)
		}
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="transparent"><title>%s</title></rect>`,
			x(i)-step/2, padT, math.Max(step, 1), innerH, tip)
	}
	// x labels
	fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" fill="currentColor" opacity="0.55" font-size="10">%s</text>`, padL, chartH-8, dayLabel(points[0].Day))
	fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" fill="currentColor" opacity="0.55" font-size="10" text-anchor="end">%s</text>`, chartW-padR, chartH-8, dayLabel(points[len(points)-1].Day))
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// niceMax rounds up to a 1/2/2.5/5 x 10^n step so the y axis reads cleanly.
func niceMax(v int64) int64 {
	if v <= 4 {
		return 4
	}
	p := math.Pow(10, math.Floor(math.Log10(float64(v))))
	for _, m := range []float64{1, 2, 2.5, 4, 5, 10} {
		if float64(v) <= m*p {
			return int64(m * p)
		}
	}
	return v
}

var weekdays = [7]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

func heatSVG(h [7][24]int64) template.HTML {
	const cell, gap = 13.0, 2.0
	var max int64
	for _, row := range h {
		for _, v := range row {
			if v > max {
				max = v
			}
		}
	}
	if max == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<svg viewBox="0 0 420 130" role="img" aria-label="Activity by weekday and hour" class="heat">`)
	for d := 0; d < 7; d++ {
		fmt.Fprintf(&b, `<text x="0" y="%.1f" font-size="9" fill="currentColor" opacity="0.55">%s</text>`, 20+float64(d)*(cell+gap)+9, weekdays[d])
		for hr := 0; hr < 24; hr++ {
			op := 0.06 + 0.9*float64(h[d][hr])/float64(max)
			tip := fmt.Sprintf("%s %02d:00 — %s messages", weekdays[d], hr, num(h[d][hr]))
			fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.0f" height="%.0f" rx="2" fill="#58a6ff" opacity="%.2f"><title>%s</title></rect>`,
				30+float64(hr)*(cell+gap), 14+float64(d)*(cell+gap), cell, cell, op, tip)
		}
	}
	for _, hr := range []int{0, 6, 12, 18} {
		fmt.Fprintf(&b, `<text x="%.1f" y="126" font-size="9" fill="currentColor" opacity="0.55">%02d</text>`,
			30+float64(hr)*(cell+gap), hr)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func bars(items []stats.Named) template.HTML {
	if len(items) == 0 {
		return ""
	}
	max := items[0].Count
	var b strings.Builder
	for _, it := range items {
		w := 0.0
		if max > 0 {
			w = 100 * float64(it.Count) / float64(max)
		}
		name := template.HTMLEscapeString(it.Name)
		fmt.Fprintf(&b, `<div class="bar"><span class="bn" title="%s">%s</span>`+
			`<span class="bt"><i style="width:%.1f%%"></i></span><b>%s</b></div>`,
			name, name, w, num(it.Count))
	}
	return template.HTML(b.String())
}

func cloud(items []stats.Named) template.HTML {
	if len(items) == 0 {
		return ""
	}
	max := items[0].Count
	var b strings.Builder
	for _, it := range items {
		scale := math.Sqrt(float64(it.Count) / float64(max))
		size := 0.8 + scale*1.15
		fmt.Fprintf(&b, `<span style="font-size:%.2frem;opacity:%.2f" title="%s">%s</span>`,
			size, 0.55+scale*0.45, num(it.Count), template.HTMLEscapeString(it.Name))
	}
	return template.HTML(b.String())
}
