// Package poortext provides rendering of text containing multiple fonts and styles.
package poortext

import (
	"image"
	"image/color"
	"unicode/utf8"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"golang.org/x/image/math/fixed"
)

// SpanStyle describes the appearance of a span of styled text.
type SpanStyle struct {
	Font    text.Font
	Size    unit.Sp
	Color   color.NRGBA
	Content string

	idx int
}

// spanShape describes the text shaping of a single span.
type spanShape struct {
	offset image.Point
	layout text.Layout
	size   image.Point
}

// Layout renders the span using the provided text shaping.
func (ss SpanStyle) Layout(gtx layout.Context, s text.Shaper, shape spanShape) layout.Dimensions {
	paint.ColorOp{Color: ss.Color}.Add(gtx.Ops)
	defer op.Offset(shape.offset).Push(gtx.Ops).Pop()
	defer clip.Outline{Path: s.Shape(ss.Font, fixed.I(gtx.Sp(ss.Size)), shape.layout)}.Op().Push(gtx.Ops).Pop()
	paint.PaintOp{}.Add(gtx.Ops)
	return layout.Dimensions{Size: shape.size}
}

// TextStyle presents rich text.
type TextStyle struct {
	Styles    []SpanStyle
	Alignment text.Alignment
	text.Shaper
}

// Text constructs a TextStyle.
func Text(shaper text.Shaper, styles ...SpanStyle) TextStyle {
	return TextStyle{
		Styles: styles,
		Shaper: shaper,
	}
}

// Layout renders the TextStyle.
//
// The spanFn function, if not nil, gets called for each span after it has been drawn,
// with an active clip region set to the span's extents.
// Its argument will be the span's index in TextStyle.Styles.
// This can be used to set up input handling, for example.
// It may get called multiple times with the same index if a span had to be broken across multiple lines.
func (t TextStyle) Layout(gtx layout.Context, spanFn func(int)) layout.Dimensions {
	spans := make([]SpanStyle, len(t.Styles))
	copy(spans, t.Styles)
	for i := range spans {
		spans[i].idx = i
	}

	var (
		lineDims       image.Point
		lineAscent     int
		overallSize    image.Point
		lineShapes     []spanShape
		lineStartIndex int
	)

	for i := 0; i < len(spans); i++ {
		// grab the next span
		span := spans[i]

		// constrain the width of the line to the remaining space
		maxWidth := gtx.Constraints.Max.X - lineDims.X

		// shape the text of the current span
		lines := t.Shaper.LayoutString(span.Font, fixed.I(gtx.Sp(span.Size)), maxWidth, gtx.Locale, span.Content)

		// grab the first line of the result and compute its dimensions
		firstLine := lines[0]
		spanWidth := firstLine.Width.Ceil()
		spanHeight := (firstLine.Ascent + firstLine.Descent).Ceil()
		spanAscent := firstLine.Ascent.Ceil()

		// forceToNextLine handles the case in which the first segment of the new span does not fit
		// AND there is already content on the current line. If there is no content on the line,
		// we should display the content that doesn't fit anyway, as it won't fit on the next
		// line either.
		forceToNextLine := lineDims.X > 0 && spanWidth > maxWidth

		if !forceToNextLine {
			// store the text shaping results for the line
			lineShapes = append(lineShapes, spanShape{
				offset: image.Point{X: lineDims.X},
				size:   image.Point{X: spanWidth, Y: spanHeight},
				layout: firstLine.Layout,
			})
			// update the dimensions of the current line
			lineDims.X += spanWidth
			if lineDims.Y < spanHeight {
				lineDims.Y = spanHeight
			}
			if lineAscent < spanAscent {
				lineAscent = spanAscent
			}

			// update the width of the overall text
			if overallSize.X < lineDims.X {
				overallSize.X = lineDims.X
			}

		}

		// if we are breaking the current span across lines or we are on the
		// last span, lay out all of the spans for the line.
		if len(lines) > 1 || i == len(spans)-1 || forceToNextLine {
			lineMacro := op.Record(gtx.Ops)
			for i, shape := range lineShapes {
				// lay out this span
				span = spans[i+lineStartIndex]
				shape.offset.Y = overallSize.Y + lineAscent
				span.Layout(gtx, t.Shaper, shape)

				if spanFn == nil {
					continue
				}
				// set this offset to the upper corner of the text, not the lower
				shape.offset.Y -= lineDims.Y
				offStack := op.Offset(shape.offset).Push(gtx.Ops)
				pr := clip.Rect(image.Rectangle{Max: shape.size}).Push(gtx.Ops)
				spanFn(span.idx)
				pr.Pop()
				offStack.Pop()
			}
			lineCall := lineMacro.Stop()

			// Compute padding to align line. If the line is longer than can be displayed then padding is implicitly
			// limited to zero.
			finalShape := lineShapes[len(lineShapes)-1]
			lineWidth := finalShape.offset.X + finalShape.size.X
			var pad int
			if lineWidth < gtx.Constraints.Max.X {
				switch t.Alignment {
				case text.Start:
					pad = 0
				case text.Middle:
					pad = (gtx.Constraints.Max.X - lineWidth) / 2
				case text.End:
					pad = gtx.Constraints.Max.X - lineWidth
				}
			}

			stack := op.Offset(image.Pt(pad, 0)).Push(gtx.Ops)
			lineCall.Add(gtx.Ops)
			stack.Pop()

			// reset line shaping data and update overall vertical dimensions
			lineShapes = lineShapes[:0]
			overallSize.Y += lineDims.Y
		}

		// if the current span breaks across lines
		if len(lines) > 1 && !forceToNextLine {
			// mark where the next line to be laid out starts
			lineStartIndex = i + 1
			lineDims = image.Point{}
			lineAscent = 0

			// ensure the spans slice has room for another span
			spans = append(spans, SpanStyle{})
			// shift existing spans further
			for k := len(spans) - 1; k > i+1; k-- {
				spans[k] = spans[k-1]
			}
			// synthesize and insert a new span
			byteLen := 0
			for i := 0; i < firstLine.Layout.Runes.Count; i++ {
				_, n := utf8.DecodeRuneInString(span.Content[byteLen:])
				byteLen += n
			}
			span.Content = span.Content[byteLen:]
			spans[i+1] = span
		} else if forceToNextLine {
			// mark where the next line to be laid out starts
			lineStartIndex = i
			lineDims = image.Point{}
			lineAscent = 0
			i--
		}
	}

	return layout.Dimensions{Size: gtx.Constraints.Constrain(overallSize)}
}
