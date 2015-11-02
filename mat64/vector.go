// Copyright ©2013 The gonum Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mat64

import (
	"github.com/gonum/blas"
	"github.com/gonum/blas/blas64"
	"github.com/gonum/internal/asm"
	"github.com/gonum/matrix"
)

var (
	vector *Vector

	_ Matrix = vector

	// _ Cloner      = vector
	// _ Viewer      = vector
	// _ Subvectorer = vector

	// _ Adder     = vector
	// _ Suber     = vector
	// _ Muler = vector
	// _ Dotter    = vector
	// _ ElemMuler = vector

	// _ Scaler  = vector
	// _ Applyer = vector

	// _ Normer = vector
	// _ Sumer  = vector

	// _ Stacker   = vector
	// _ Augmenter = vector

	// _ Equaler       = vector
	// _ ApproxEqualer = vector

	// _ RawMatrixLoader = vector
	// _ RawMatrixer     = vector
)

// Vector represents a column vector.
type Vector struct {
	mat blas64.Vector
	n   int
	// A BLAS vector can have a negative increment, but allowing this
	// in the mat64 type complicates a lot of code, and doesn't gain anything.
	// Vector must have positive increment in this package.
}

// NewVector creates a new Vector of length n. If len(data) == n, data is used
// as the backing data slice. If data == nil, a new slice is allocated. If
// neither of these is true, NewVector will panic.
func NewVector(n int, data []float64) *Vector {
	if len(data) != n && data != nil {
		panic(matrix.ErrShape)
	}
	if data == nil {
		data = make([]float64, n)
	}
	return &Vector{
		mat: blas64.Vector{
			Inc:  1,
			Data: data,
		},
		n: n,
	}
}

// ViewVec returns a sub-vector view of the receiver starting at element i and
// extending n rows. If i is out of range, n is zero, or the view extends
// beyond the bounds of the Vector, ViewVec will panic with ErrIndexOutOfRange.
// The returned Vector retains reference to the underlying vector.
func (v *Vector) ViewVec(i, n int) *Vector {
	if i < 0 || n <= 0 || i+n > v.n {
		panic(matrix.ErrIndexOutOfRange)
	}
	return &Vector{
		n: n,
		mat: blas64.Vector{
			Inc:  v.mat.Inc,
			Data: v.mat.Data[i*v.mat.Inc : (i+n-1)*v.mat.Inc+1],
		},
	}
}

func (v *Vector) Dims() (r, c int) {
	if v.isZero() {
		return 0, 0
	}
	return v.n, 1
}

// Len returns the length of the vector.
func (v *Vector) Len() int {
	return v.n
}

// T performs an implicit transpose by returning the receiver inside a Transpose.
func (v *Vector) T() Matrix {
	return Transpose{v}
}

// Reset zeros the length of the vector so that it can be reused as the
// receiver of a dimensionally restricted operation.
//
// See the Reseter interface for more information.
func (v *Vector) Reset() {
	// No change of Inc or n to 0 may be
	// made unless both are set to 0.
	v.mat.Inc = 0
	v.n = 0
	v.mat.Data = v.mat.Data[:0]
}

// CloneVec makes a copy of a into the receiver, overwriting the previous value
// of the receiver.
func (v *Vector) CloneVec(a *Vector) {
	if v == a {
		return
	}
	v.n = a.n
	v.mat = blas64.Vector{
		Inc:  1,
		Data: use(v.mat.Data, v.n),
	}
	blas64.Copy(v.n, a.mat, v.mat)
}

func (v *Vector) RawVector() blas64.Vector {
	return v.mat
}

// CopyVec makes a copy of elements of a into the receiver. It is similar to the
// built-in copy; it copies as much as the overlap between the two vectors and
// returns the number of elements it copied.
func (v *Vector) CopyVec(a *Vector) int {
	n := min(v.Len(), a.Len())
	if v != a {
		blas64.Copy(n, a.mat, v.mat)
	}
	return n
}

// ScaleVec scales the vector a by alpha, placing the result in the receiver.
func (v *Vector) ScaleVec(alpha float64, a *Vector) {
	n := a.Len()
	if v != a {
		v.reuseAs(n)
		blas64.Copy(n, a.mat, v.mat)
	}
	if alpha != 1 {
		blas64.Scal(n, alpha, v.mat)
	}
}

// AddScaledVec adds the vectors a and alpha*b, placing the result in the receiver.
func (v *Vector) AddScaledVec(a *Vector, alpha float64, b *Vector) {
	if alpha == 1 {
		v.AddVec(a, b)
		return
	}
	if alpha == -1 {
		v.SubVec(a, b)
		return
	}

	ar := a.Len()
	br := b.Len()

	if ar != br {
		panic(matrix.ErrShape)
	}

	v.reuseAs(ar)

	if alpha == 0 {
		v.CopyVec(a)
		return
	}

	switch {
	case v == a && v == b: // v <- v + alpha * v = (alpha + 1) * v
		blas64.Scal(ar, alpha+1, v.mat)
	case v == a && v != b: // v <- v + alpha * b
		blas64.Axpy(ar, alpha, b.mat, v.mat)
	case v != a && v == b: // v <- a + alpha * v
		if v.mat.Inc == 1 && a.mat.Inc == 1 {
			// Fast path for a common case.
			v := v.mat.Data
			for i, a := range a.mat.Data {
				v[i] *= alpha
				v[i] += a
			}
			return
		}
		blas64.Scal(ar, alpha, v.mat)
		blas64.Axpy(ar, 1, a.mat, v.mat)
	default: // v <- a + alpha * b
		if v.mat.Inc == 1 && a.mat.Inc == 1 && b.mat.Inc == 1 {
			// Fast path for a common case.
			asm.DaxpyUnitary(alpha, b.mat.Data, a.mat.Data, v.mat.Data)
			return
		}
		blas64.Copy(ar, a.mat, v.mat)
		blas64.Axpy(ar, alpha, b.mat, v.mat)
	}
}

// AddVec adds a and b element-wise, placing the result in the receiver.
func (v *Vector) AddVec(a, b *Vector) {
	ar := a.Len()
	br := b.Len()

	if ar != br {
		panic(matrix.ErrShape)
	}

	v.reuseAs(ar)

	amat, bmat := a.RawVector(), b.RawVector()
	for i := 0; i < v.n; i++ {
		v.mat.Data[i*v.mat.Inc] = amat.Data[i*amat.Inc] + bmat.Data[i*bmat.Inc]
	}
}

// SubVec subtracts the vector b from a, placing the result in the receiver.
func (v *Vector) SubVec(a, b *Vector) {
	ar := a.Len()
	br := b.Len()

	if ar != br {
		panic(matrix.ErrShape)
	}

	v.reuseAs(ar)

	amat, bmat := a.RawVector(), b.RawVector()
	for i := 0; i < v.n; i++ {
		v.mat.Data[i*v.mat.Inc] = amat.Data[i*amat.Inc] - bmat.Data[i*bmat.Inc]
	}
}

// MulElemVec performs element-wise multiplication of a and b, placing the result
// in the receiver.
func (v *Vector) MulElemVec(a, b *Vector) {
	ar := a.Len()
	br := b.Len()

	if ar != br {
		panic(matrix.ErrShape)
	}

	v.reuseAs(ar)

	amat, bmat := a.RawVector(), b.RawVector()
	for i := 0; i < v.n; i++ {
		v.mat.Data[i*v.mat.Inc] = amat.Data[i*amat.Inc] * bmat.Data[i*bmat.Inc]
	}
}

// DivElemVec performs element-wise division of a by b, placing the result
// in the receiver.
func (v *Vector) DivElemVec(a, b *Vector) {
	ar := a.Len()
	br := b.Len()

	if ar != br {
		panic(matrix.ErrShape)
	}

	v.reuseAs(ar)

	amat, bmat := a.RawVector(), b.RawVector()
	for i := 0; i < v.n; i++ {
		v.mat.Data[i*v.mat.Inc] = amat.Data[i*amat.Inc] / bmat.Data[i*bmat.Inc]
	}
}

// MulVec computes a * b. The result is stored into the receiver.
// MulVec panics if the number of columns in a does not equal the number of rows in b.
func (v *Vector) MulVec(a Matrix, b *Vector) {
	r, c := a.Dims()
	br := b.Len()
	if c != br {
		panic(matrix.ErrShape)
	}
	a, trans := untranspose(a)
	ar, ac := a.Dims()
	v.reuseAs(r)
	var restore func()
	if v == a {
		v, restore = v.isolatedWorkspace(a.(*Vector))
		defer restore()
	} else if v == b {
		v, restore = v.isolatedWorkspace(b)
		defer restore()
	}

	switch a := a.(type) {
	case *Vector:
		if a.Len() == 1 {
			// {1,1} x {1,n}
			av := a.At(0, 0)
			for i := 0; i < b.Len(); i++ {
				v.mat.Data[i*v.mat.Inc] = av * b.mat.Data[i*b.mat.Inc]
			}
			return
		}
		if b.Len() == 1 {
			// {1,n} x {1,1}
			bv := b.At(0, 0)
			for i := 0; i < a.Len(); i++ {
				v.mat.Data[i*v.mat.Inc] = bv * a.mat.Data[i*a.mat.Inc]
			}
			return
		}
		// {n,1} x {1,n}
		var sum float64
		for i := 0; i < c; i++ {
			sum += a.At(i, 0) * b.At(i, 0)
		}
		v.SetVec(0, sum)
		return
	case RawSymmetricer:
		amat := a.RawSymmetric()
		blas64.Symv(1, amat, b.mat, 0, v.mat)
	case RawTriangular:
		v.CopyVec(b)
		amat := a.RawTriangular()
		ta := blas.NoTrans
		if trans {
			ta = blas.Trans
		}
		blas64.Trmv(ta, amat, v.mat)
	case RawMatrixer:
		amat := a.RawMatrix()
		t := blas.NoTrans
		if trans {
			t = blas.Trans
		}
		blas64.Gemv(t, 1, amat, b.mat, 0, v.mat)
	case Vectorer:
		if trans {
			col := make([]float64, ar)
			for c := 0; c < ac; c++ {
				v.mat.Data[c*v.mat.Inc] = blas64.Dot(ar,
					blas64.Vector{Inc: 1, Data: a.Col(col, c)},
					b.mat,
				)
			}
		} else {
			row := make([]float64, ac)
			for r := 0; r < ar; r++ {
				v.mat.Data[r*v.mat.Inc] = blas64.Dot(ac,
					blas64.Vector{Inc: 1, Data: a.Row(row, r)},
					b.mat,
				)
			}
		}
	default:
		if trans {
			col := make([]float64, ar)
			for c := 0; c < ac; c++ {
				for i := range col {
					col[i] = a.At(i, c)
				}
				var f float64
				for i, e := range col {
					f += e * b.mat.Data[i*b.mat.Inc]
				}
				v.mat.Data[c*v.mat.Inc] = f
			}
		} else {
			row := make([]float64, ac)
			for r := 0; r < ar; r++ {
				for i := range row {
					row[i] = a.At(r, i)
				}
				var f float64
				for i, e := range row {
					f += e * b.mat.Data[i*b.mat.Inc]
				}
				v.mat.Data[r*v.mat.Inc] = f
			}
		}
	}
}

// reuseAs resizes an empty vector to a r×1 vector,
// or checks that a non-empty matrix is r×1.
func (v *Vector) reuseAs(r int) {
	if v.isZero() {
		v.mat = blas64.Vector{
			Inc:  1,
			Data: use(v.mat.Data, r),
		}
		v.n = r
		return
	}
	if r != v.n {
		panic(matrix.ErrShape)
	}
}

func (v *Vector) isZero() bool {
	// It must be the case that v.Dims() returns
	// zeros in this case. See comment in Reset().
	return v.mat.Inc == 0
}

func (v *Vector) isolatedWorkspace(a *Vector) (n *Vector, restore func()) {
	l := a.Len()
	n = getWorkspaceVec(l, false)
	return n, func() {
		v.CopyVec(n)
		putWorkspaceVec(n)
	}
}
