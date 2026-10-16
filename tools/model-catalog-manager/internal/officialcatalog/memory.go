package officialcatalog

func zero(data []byte) {
	for i := range data {
		data[i] = 0
	}
}
