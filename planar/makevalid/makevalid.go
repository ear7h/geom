package makevalid

import (
	"context"
	"errors"
	"log"
	"sort"

	"github.com/go-spatial/geom"
	"github.com/go-spatial/geom/cmp"
	"github.com/go-spatial/geom/encoding/wkt"
	"github.com/go-spatial/geom/planar"
	"github.com/go-spatial/geom/planar/intersect"
	"github.com/go-spatial/geom/planar/makevalid/hitmap"
	"github.com/go-spatial/geom/planar/makevalid/walker"
)

type Makevalid struct {
	Hitmap planar.HitMapper
	// Currently not used, but once we have the IsValid function, we can use this instead
	// Of running the MakeValid routine on a Geometry that is alreayd valid.
	// Used to clip geometries that are not Polygon and MultiPolygons
	Clipper planar.Clipper
}

// asSegments calls the AsSegments functions and flattens the array of segments that are returned.
func asSegments(g geom.Geometry) (segs []geom.Line, err error) {
	switch g := g.(type) {
	case geom.LineString:
		return g.AsSegments()
	case geom.MultiLineString:
		s, err := g.AsSegments()
		if err != nil {
			return nil, err
		}
		for i := range s {
			segs = append(segs, s[i]...)
		}
		return segs, nil
	case geom.Polygon:
		s, err := g.AsSegments()
		if err != nil {
			return nil, err
		}
		for i := range s {
			segs = append(segs, s[i]...)
		}
		return segs, nil
	case geom.MultiPolygon:
		s, err := g.AsSegments()
		if err != nil {
			return nil, err
		}
		for i := range s {
			for j := range s[i] {
				segs = append(segs, s[i][j]...)
			}
		}
		return segs, nil
	}
	return nil, errors.New("Unsupported")
}

type ByXYPoint [][2]float64

func (a ByXYPoint) Len() int           { return len(a) }
func (a ByXYPoint) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByXYPoint) Less(i, j int) bool { return cmp.PointLess(a[i], a[j]) }

type ByXYLine []geom.Line

func (a ByXYLine) Len() int      { return len(a) }
func (a ByXYLine) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a ByXYLine) Less(i, j int) bool {
	return cmp.PointLess(a[i][0], a[j][0]) || cmp.PointLess(a[i][1], a[j][1])
}

// filterLinesbyClipbox will iterate through all the given lines returning only lines that
// intersect with the clipbox. A nil clipbox is assumed to indicate a clipbox containing the
// whole universe, and so all lines are returned unfiltered.
func filterLinesByClipbox(clipbox *geom.Extent, lines []geom.Line) (nlines []geom.Line) {

	if clipbox == nil {
		nlines = make([]geom.Line, len(lines))
		copy(nlines, lines)
		return nlines
	}

	for _, ln := range lines {
		gext, err := geom.NewExtentFromGeometry(ln)
		if err != nil {
			log.Printf("Got an error trying to find the extent of %v: %v", wkt.MustEncode(ln), err)
			continue
		}
		if _, ok := clipbox.Intersect(gext); ok {
			// Keep lines that intersect our clipbox in some way.
			nlines = append(nlines, ln)
		}
	}
	return nlines
}

// splitIntersectingLines will split the given set of lines so that in any lines that intersect
// each other are split into new lines. If a clipbox is provided (not nil) then any line not
// wholly contained by the clip box will be discarded.
func splitIntersectingLines(clipbox *geom.Extent, lines []geom.Line) (nlines []geom.Line) {

	ctx := context.Background()
	matchEndPoint := func(l geom.Line, pt [2]float64) bool {
		return cmp.PointEqual(pt, l[0]) || cmp.PointEqual(pt, l[1])
	}

	// This will hold all the points that will make up the parts of the lines
	// to split, not including the end points.
	newPartialSegments := make([][][2]float64, len(lines))

	eq := intersect.NewEventQueue(lines)
	eq.FindIntersects(ctx, true, func(src, dst int, pt [2]float64) error {
		if !matchEndPoint(lines[src], pt) {
			newPartialSegments[src] = append(newPartialSegments[src], pt)
		}
		if !matchEndPoint(lines[dst], pt) {
			newPartialSegments[dst] = append(newPartialSegments[dst], pt)
		}
		return nil
	})
	for idx, pseg := range newPartialSegments {

		// Sort points
		sort.Sort(ByXYPoint(pseg))

		if debug {
			log.Printf("Points for %v -- %v", idx, pseg)
			log.Printf("For %04v: %v", idx, wkt.MustEncode(lines[idx]))
		}

		// Generate new segments
		i := 0
		for j := 1; j < len(pseg); j++ {
			if cmp.PointEqual(pseg[i], pseg[j]) {
				continue
			}
			// found a line
			ln := [2][2]float64{pseg[i], pseg[j]}
			if debug {
				log.Printf("\t Would add (%v,%v): %v", i, j, wkt.MustEncode(geom.Line(ln)))
			}
			i = j
			if clipbox != nil && !clipbox.ContainsLine(ln) {
				// Does not intersect the clipbox skip
				if debug {
					log.Println("did not add")
				}
				continue
			}
			nlines = append(nlines, geom.Line(ln))
		}
	}
	return nlines
}

// Destructure will take a multipolygon, break up the polygon into a set of segments that have the following characteristics:
// 1. no segment will intersect with another segment, other then at the end points; or colinear and partial-coliner lines.
// 2. normalize direction of line segments to left to right
// 3. line segments are generally unique.
// 4. line segments outside of the clipbox will be clipped
func Destructure(ctx context.Context, clipbox *geom.Extent, multipolygon *geom.MultiPolygon) ([]geom.Line, error) {

	segments, err := asSegments(*multipolygon)
	if err != nil {
		if debug {
			log.Printf("asSegments returned error: %v", err)
		}
		return nil, err
	}
	if debug {
		log.Printf("The original segments are:\n")
		for i, seg := range segments {
			log.Printf("\t%03d:%s\n", i, wkt.MustEncode(seg))
		}
	}
	gext, err := geom.NewExtentFromGeometry(multipolygon)
	if err != nil {
		return nil, err
	}

	// Let's see if our clip box is bigger then our polygon.
	// if it is we don't need the clip box.
	hasClipbox := clipbox != nil && !clipbox.Contains(gext)
	if debug {
		log.Printf("\thasClipbox: %v && !%v", clipbox != nil, clipbox.Contains(gext))
	}
	var clpbx *geom.Extent
	// Let's get the edges of our clipbox; as segments and add it to the begining.
	if hasClipbox {
		var ok bool
		// Let get the intersection of the clipbox and the extent of the geometry.
		if clpbx, ok = clipbox.Intersect(gext); !ok {
			if debug {
				log.Printf("\tGeometry Extent does not intersect the clipbox?\nGeometry Extent:%#v\n%v\n clipbox Extent:%#v\n%v\n ", gext, wkt.MustEncode(gext), clipbox, wkt.MustEncode(clipbox))
			}
			return []geom.Line{}, nil
		}
		filteredSegments := filterLinesByClipbox(clipbox, segments)

		edges := clpbx.Edges(nil)
		segments = append([]geom.Line{
			geom.Line(edges[0]), geom.Line(edges[1]),
			geom.Line(edges[2]), geom.Line(edges[3]),
		}, filteredSegments...)
	}

	if debug {
		log.Printf("The segments to work on are:\n")
		for i, seg := range segments {
			log.Printf("\t%03d:%s\n", i, wkt.MustEncode(seg))
		}
	}
	nsegs := splitIntersectingLines(clipbox, segments)
	if debug {
		log.Printf("The new segments after split are:\n")
		for i, seg := range nsegs {
			log.Printf("\t%03d:%s\n", i, wkt.MustEncode(seg))
		}
	}

	return nsegs, nil
}

// keepShortestOfSameSlopeSPoint will keep the frist line with the shortest length
// within a set of lines with the same slope and start point.
func keepShortestOfSameSlopeSPoint(segs []geom.Line) (newSegs []geom.Line) {
	if debug {
		for i, seg := range segs {
			log.Printf("starting seg(% 4d):%v", i, wkt.MustEncode(geom.Line(seg)))
		}
	}

	type key struct {
		x, y, slope  float64
		slopeDefined bool
	}
	type value struct {
		idx    int
		length float64
	}
	lineKeeper := make(map[key]value)
	keyForLine := func(l geom.Line, ipt, jpt int) key {
		s, _, sd := planar.Slope([2][2]float64{l[ipt], l[jpt]})
		return key{
			x:            l[ipt][0],
			y:            l[ipt][1],
			slope:        s,
			slopeDefined: sd,
		}
	}
	for i, seg := range segs {
		shouldAppend := true
		idx := len(newSegs)
		ipt, jpt := 0, 1
		// find the top left most point of the line.
		if !cmp.PointLess(seg[0], seg[1]) {
			ipt, jpt = 1, 0
		}
		segKey := keyForLine(seg, ipt, jpt)
		length := planar.FloatPointDistance2(seg[ipt], seg[jpt])
		// look at the distance to see if we should keep
		// the segment that in there.
		if cseg, ok := lineKeeper[segKey]; ok && cseg.length <= length {
			continue
		} else if ok {
			shouldAppend = false
			idx = cseg.idx
		}
		if shouldAppend {
			newSegs = append(newSegs, segs[i])
		} else {
			log.Printf("idx: %v len newSegs %v", idx, len(newSegs))
			newSegs[idx] = segs[i]
		}
		lineKeeper[segKey] = value{
			idx:    idx,
			length: length,
		}
	}
	if debug {
		for i, seg := range newSegs {
			log.Printf("ending seg(% 4d):%v", i, wkt.MustEncode(geom.Line(seg)))
		}
	}
	return newSegs
}

// unique sorts segments by XY and filters out duplicate segments.
func unique(segs []geom.Line) {
	sort.Sort(ByXYLine(segs))

	// we can use a slice trick to avoid copying the array again. Maybe better
	// than two index variables...
	uniqued := segs[:0]
	for i := 0; i < len(segs); i++ {
		if i == 0 || !(cmp.PointEqual(segs[i][0], segs[i-1][0]) && cmp.PointEqual(segs[i][1], segs[i-1][1])) {
			uniqued = append(uniqued, segs[i])
		}
	}
	// uniqued is backed by segs, no need to return it
}

func (mv *Makevalid) makevalidPolygon(ctx context.Context, clipbox *geom.Extent, multipolygon *geom.MultiPolygon) (*geom.MultiPolygon, error) {
	if debug {
		log.Printf("*Step  1 : Destructure the geometry into segments w/ the clipbox applied.")
	}
	segs, err := Destructure(ctx, clipbox, multipolygon)
	if err != nil {
		if debug {
			log.Printf("Destructure returned err %v", err)
		}
		return nil, err
	}
	if len(segs) == 0 {
		if debug {
			log.Printf("Step   1a: Segments are zero.")
			log.Printf("\t multiPolygon: %+v", multipolygon)
			log.Printf("\n clipbox:      %+v", clipbox)
		}
		return nil, nil
	}
	if debug {
		log.Printf("Step   2 : Convert segments to linestrings to use in triangleuation.")
	}

	if debug {
		log.Printf("triangulating segs(%v)\n", len(segs))
		for i, seg := range segs {
			log.Printf("seg(% 4d):%v", i, wkt.MustEncode(geom.Line(seg)))
		}
	}

	hm, err := hitmap.NewFromPolygons(nil, (*multipolygon)...)
	if err != nil {
		return nil, err
	}

	triangles, err := InsideTrianglesForGeometry(ctx, segs, hm)
	if err != nil {
		return nil, err
	}
	if debug {
		log.Printf("Step   5 : generate multipolygon from triangles")
	}
	triWalker := walker.New(triangles)
	mplygs := triWalker.MultiPolygon(ctx)

	return &mplygs, nil
}

func (mv *Makevalid) Makevalid(ctx context.Context, geo geom.Geometry, clipbox *geom.Extent) (geometry geom.Geometry, didClip bool, err error) {

	switch g := geo.(type) {

	case geom.LineStringer, geom.MultiLineStringer, geom.Pointer, geom.MultiPointer:
		if mv.Clipper != nil {
			gg, err := mv.Clipper.Clip(ctx, geo, clipbox)
			if err != nil {
				return nil, false, err
			}
			return gg, true, nil
		}
		return geo, false, nil
	case geom.Polygoner:
		if debug {
			log.Printf("Working on Polygoner: %v", geo)
		}
		mp := geom.MultiPolygon{g.LinearRings()}
		vmp, err := mv.makevalidPolygon(ctx, clipbox, &mp)
		if err != nil {
			return nil, false, err
		}
		if debug {
			log.Printf("Returning on Polygon: %T", vmp)
		}
		return vmp, true, nil
	case geom.MultiPolygoner:
		if debug {
			log.Printf("Working on MultiPolygoner: %v", geo)
		}
		mp := geom.MultiPolygon(g.Polygons())
		vmp, err := mv.makevalidPolygon(ctx, clipbox, &mp)
		if err != nil {
			return nil, false, err
		}
		if debug {
			log.Printf("Returning on MultiPolygon: %T", vmp)
		}
		return vmp, true, nil
	}
	if debug {
		log.Printf("Got an unknown geometry %T", geo)
	}
	return nil, false, geom.ErrUnknownGeometry{geo}

}
