package geom

import (
	"math"
)

// Extenter represents an interface that returns a boundbox.
type Extenter interface {
	Extent() (extent [4]float64)
}

type MinMaxer interface {
	MinX() float64
	MinY() float64
	MaxX() float64
	MaxY() float64
}

// Extent represents the minx, miny, maxx and maxy
// A nil extent represents the whole universe.
type Extent [4]float64

/* ========================= ATTRIBUTES ========================= */

// Vertices return the verticies of the Bounding Box. The verticies are ordered in the following maner.
// (minx,miny), (maxx,miny), (maxx,maxy), (minx,maxy)
func (e *Extent) Vertices() [][2]float64 {
	return [][2]float64{
		{e.MinX(), e.MinY()},
		{e.MaxX(), e.MinY()},
		{e.MaxX(), e.MaxY()},
		{e.MinX(), e.MaxY()},
	}
}

// ClockwiseFunc returns weather the set of points should be considered clockwise or counterclockwise. The last point is not the same as the first point, and the function should connect these points as needed.
type ClockwiseFunc func(...[2]float64) bool

// Edges returns the clockwise order of the edges that make up the extent.
func (e *Extent) Edges(cwfn ClockwiseFunc) [][2][2]float64 {
	v := e.Vertices()
	if cwfn != nil && !cwfn(v...) {
		v[0], v[1], v[2], v[3] = v[3], v[2], v[1], v[0]
	}
	return [][2][2]float64{
		{v[0], v[1]},
		{v[1], v[2]},
		{v[2], v[3]},
		{v[3], v[0]},
	}
}

// MaxX is the larger of the x values.
func (e *Extent) MaxX() float64 {
	if e == nil {
		return math.MaxFloat64
	}
	return e[2]
}

// MinX  is the smaller of the x values.
func (e *Extent) MinX() float64 {
	if e == nil {
		return -math.MaxFloat64
	}
	return e[0]
}

// MaxY is the larger of the y values.
func (e *Extent) MaxY() float64 {
	if e == nil {
		return math.MaxFloat64
	}
	return e[3]
}

// MinY is the smaller of the y values.
func (e *Extent) MinY() float64 {
	if e == nil {
		return -math.MaxFloat64
	}
	return e[1]
}

// Min returns the (MinX, MinY) values
func (e *Extent) Min() [2]float64 {
	return [2]float64{e[0], e[1]}
}

// Max returns the (MaxX, MaxY) values
func (e *Extent) Max() [2]float64 {
	return [2]float64{e[2], e[3]}
}

// TODO (gdey): look at how to have this function take into account the dpi.
func (e *Extent) XSpan() float64 {
	if e == nil {
		return math.Inf(1)
	}
	return e[2] - e[0]
}
func (e *Extent) YSpan() float64 {
	if e == nil {
		return math.Inf(1)
	}
	return e[3] - e[1]
}

func (e *Extent) Extent() [4]float64 {
	return [4]float64{e.MinX(), e.MinY(), e.MaxX(), e.MaxY()}
}

/* ========================= EXPANDING BOUNDING BOX ========================= */
// Add will expand the extent to contain the given extent.
func (e *Extent) Add(extent MinMaxer) {
	if e == nil {
		return
	}
	e[0] = math.Min(e[0], extent.MinX())
	e[2] = math.Max(e[2], extent.MaxX())
	e[1] = math.Min(e[1], extent.MinY())
	e[3] = math.Max(e[3], extent.MaxY())
}

// AddPoints will expand the extent to contain the given points.
func (e *Extent) AddPoints(points ...[2]float64) {
	// A nil extent is all encompassing.
	if e == nil {
		return
	}
	if len(points) == 0 {
		return
	}
	for _, pt := range points {
		e[0] = math.Min(pt[0], e[0])
		e[1] = math.Min(pt[1], e[1])
		e[2] = math.Max(pt[0], e[2])
		e[3] = math.Max(pt[1], e[3])
	}
}

func (e *Extent) AddPointers(pts ...Pointer) {
	for i := range pts {
		e.AddPoints(pts[i].XY())
	}
}

// AddPointer expands the specified envelop to contain p.
func (e *Extent) AddGeometry(g Geometry) error {
	return getExtent(g, e)
}

// AsPolygon will return the extent as a Polygon
func (e *Extent) AsPolygon() Polygon { return Polygon{e.Vertices()} }

// Area returns the area of the extent, if the extent is nil, it will return 0
func (e *Extent) Area() float64 {
	return math.Abs((e.MaxY() - e.MinY()) * (e.MaxX() - e.MinX()))
}

// Hull returns the smallest extent from lon/lat points.
// The hull is defined in the following order [4]float64{ West, South, East, North}
func Hull(a, b [2]float64) *Extent {
	// lat <=> y
	// lng <=> x

	// make a the westmost point
	if math.Abs(a[0]-b[0]) > 180.0 {
		// smallest longitudinal arc crosses the antimeridian
		if a[0] < b[0] {
			a[0], b[0] = b[0], a[0]
		}
	} else {
		if a[0] > b[0] {
			a[0], b[0] = b[0], a[0]
		}
	}

	return Segment(a, b)
}

// Segment of a sphere from two long/lat points, with a being the westmost point and b the eastmost point; in following format [4]float64{ West, South, East, North }
func Segment(westy, easty [2]float64) *Extent {
	north, south := westy[1], easty[1]
	if north < south {
		south, north = north, south
	}

	return &Extent{westy[0], south, easty[0], north}
}

// NewExtent returns an Extent for the provided points; in following format [4]float64{ MinX, MinY, MaxX, MaxY }
func NewExtent(points ...[2]float64) *Extent {
	var xy [2]float64
	if len(points) == 0 {
		return nil
	}

	extent := Extent{points[0][0], points[0][1], points[0][0], points[0][1]}
	if len(points) == 1 {
		return &extent
	}
	for i := 1; i < len(points); i++ {
		xy = points[i]
		// Check the x coords
		switch {
		case xy[0] < extent[0]:
			extent[0] = xy[0]
		case xy[0] > extent[2]:
			extent[2] = xy[0]
		}
		// Check the y coords
		switch {
		case xy[1] < extent[1]:
			extent[1] = xy[1]
		case xy[1] > extent[3]:
			extent[3] = xy[1]
		}
	}
	return &extent
}

func NewExtentFromGeometry(g Geometry) (*Extent, error) {
	e := Extent{}
	err := getExtent(g, &e)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// Contains will return whether the given  extent is inside of the  extent.
func (e *Extent) Contains(ne MinMaxer) bool {
	// Nil extent contains the world.
	if e == nil {
		return true
	}
	if ne == nil {
		return false
	}
	return e.MinX() <= ne.MinX() &&
		e.MaxX() >= ne.MaxX() &&
		e.MinY() <= ne.MinY() &&
		e.MaxY() >= ne.MaxY()
}

// ContainsPoint will return whether the given point is inside of the extent.
func (e *Extent) ContainsPoint(pt [2]float64) bool {
	if e == nil {
		return true
	}
	return e.MinX() <= pt[0] && pt[0] <= e.MaxX() &&
		e.MinY() <= pt[1] && pt[1] <= e.MaxY()
}

// ContainsLine will return weather the given line completely inside of the extent.
func (e *Extent) ContainsLine(l [2][2]float64) bool {
	if e == nil {
		return true
	}
	return e.ContainsPoint(l[0]) && e.ContainsPoint(l[1])
}

// ContainsGeom will return weather the given geometry is completely inside of the extent.
func (e *Extent) ContainsGeom(g Geometry) (bool, error) {
	if e.IsUniverse() {
		return true, nil
	}
	// Check to see if it can be a MinMaxer, if so use that.
	if extenter, ok := g.(MinMaxer); ok {
		return e.Contains(extenter), nil
	}
	// we will use a exntent that contains the geometry, and check to see if this extent contains that extent.
	var ne = new(Extent)
	if err := ne.AddGeometry(g); err != nil {
		return false, err
	}
	return e.Contains(ne), nil
}

// ScaleBy will scale the points in the extent by the given scale factor.
func (e *Extent) ScaleBy(s float64) *Extent {
	if e == nil {
		return nil
	}
	return NewExtent(
		[2]float64{e[0] * s, e[1] * s},
		[2]float64{e[2] * s, e[3] * s},
	)
}

// ExpandBy will expand extent by the given factor.
func (e *Extent) ExpandBy(s float64) *Extent {
	if e == nil {
		return nil
	}
	return NewExtent(
		[2]float64{e[0] - s, e[1] - s},
		[2]float64{e[2] + s, e[3] + s},
	)
}

func (e *Extent) Clone() *Extent {
	if e == nil {
		return nil
	}
	return &Extent{e[0], e[1], e[2], e[3]}
}

// Intersect will return a new extent that is the intersect of the two extents.
//
// +-------------------------+
// |                         |
// |       A      +----------+------+
// |              |//////////|      |
// |              |/// C ////|      |
// |              |//////////|      |
// +--------------+----------+      |
//                |             B   |
//                +-----------------+
// For example the for the above Box A intersects Box B at the area surround by C.
//
// If the Boxes don't intersect does will be false, otherwise ibb will be the intersect.
func (e *Extent) Intersect(ne *Extent) (*Extent, bool) {
	// if e in nil, then the intersect is ne. As a nil extent is the whole universe.
	if e == nil {
		return ne.Clone(), true
	}
	// if ne is nil, then the intersect is e. As a nil extent is the whole universe.
	if ne == nil {
		return e.Clone(), true
	}

	minx := e.MinX()
	if minx < ne.MinX() {
		minx = ne.MinX()
	}
	maxx := e.MaxX()
	if maxx > ne.MaxX() {
		maxx = ne.MaxX()
	}
	// The boxes don't intersect.
	if minx >= maxx {
		return nil, false
	}
	miny := e.MinY()
	if miny < ne.MinY() {
		miny = ne.MinY()
	}
	maxy := e.MaxY()
	if maxy > ne.MaxY() {
		maxy = ne.MaxY()
	}

	// The boxes don't intersect.
	if miny >= maxy {
		return nil, false
	}
	return &Extent{minx, miny, maxx, maxy}, true
}

// IsUniverse returns weather the extent contains the universe. This is true if the clip box is nil or the x,y values are max values.
func (e *Extent) IsUniverse() bool {
	return e == nil || (e.MinX() == -math.MaxFloat64 && e.MaxX() == math.MaxFloat64 &&
		e.MinY() == -math.MaxFloat64 && e.MaxY() == math.MaxFloat64)
}
