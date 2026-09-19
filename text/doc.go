// Package text finds system fonts and draws text with them onto a canvas.
//
// It is the bridge between the fonts a user has installed and the
// widget.Font interface the widgets ask for. It exists as its own package,
// rather than living in canvas or widget, because it is the only place that
// needs golang.org/x/image's font rasterizer.
//
// # Finding a font
//
// There is no fontconfig here: that would mean cgo. [Find] scans the
// directories fontconfig itself would scan — $XDG_DATA_HOME/fonts,
// ~/.fonts and every $XDG_DATA_DIRS entry's fonts directory — and matches by
// the family and style names stored inside the font files, not by file
// name. It reads only each file's table directory and name table, so
// scanning some 800 fonts takes about 15 ms; still, do it once at startup,
// not per frame.
//
// # Drawing
//
// A [Face] is a font at a size in logical units. It rasterizes at the
// canvas's scale — a 16-unit face on a 2x canvas draws glyphs 32 pixels
// tall — so text stays sharp on HiDPI instead of being a scaled-up bitmap.
// The glyph outlines are unhinted, which is what lets [Face.Measure] answer
// in logical units without knowing the scale: advances are linear in the
// size.
//
// # Damage
//
// canvas has no text API, so a Face writes glyph coverage straight into the
// pixels the canvas borrowed. That is within the borrow contract, but those
// pixels are invisible to Canvas.Damage. A caller that draws text must
// damage the region itself, or the whole buffer.
//
// # Limits
//
// Single-line, left-to-right text only: no shaping, no bidirectional
// reordering, no fallback fonts. Kerning pairs are applied; ligatures and
// complex scripts are not. Control characters are skipped.
//
// A Face is not safe for concurrent use.
package text
