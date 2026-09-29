package normalize

import (
	"slices"
	"testing"
)

func TestDistances(t *testing.T) {
	tests := []struct {
		in   string
		want []int
	}{
		{"21,1 км", []int{21100}},
		{"полумарафон", []int{21098}},
		{"Полумарафон", []int{21098}},
		{"марафон", []int{42195}},
		{"42.2 km", []int{42200}},
		{"42,195 км", []int{42195}},
		{"21.0975 km", []int{21098}},
		{"5 км, 10 км, 21,1 км", []int{5000, 10000, 21100}},
		{"10км", []int{10000}},
		{"500 м", []int{500}},
		{"5000м", []int{5000}},
		{"10 КМ", []int{10000}},
		{"10 км и полумарафон", []int{10000, 21098}},
		{"10 км, 10 км", []int{10000}},
		{"Московская область, Химки, 10 км", []int{10000}},
		{"1 час", nil},
		{"марафонец", nil},
		{"10 марафонов", nil},
		{"10 мин", nil},
		{"0 км", nil},
		{"99999999999 км", nil}, // does not fit event_distances.distance_m INTEGER
		{"", nil},
	}
	for _, tt := range tests {
		if got := Distances(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("Distances(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
