package webp

import "sort"

func vp8lPaletteOrders(table []uint32) [][]uint32 {
	orders := [][]uint32{table}
	appendUnique := func(candidate []uint32) {
		for _, order := range orders {
			if vp8lUint32SlicesEqual(order, candidate) {
				return
			}
		}
		orders = append(orders, candidate)
	}
	sorted := append([]uint32(nil), table...)
	sort.Slice(sorted, func(i int, j int) bool { return sorted[i] < sorted[j] })
	appendUnique(sorted)
	return orders
}
