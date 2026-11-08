package nativeharness

// PrepareNativeStream changes transport only, never the native prompt or task.
// Both encodings normalize to the same ledger digest. Unknown fields, duplicate
// keys and unsupported request semantics still fail through the strict parser.
func PrepareNativeStream(raw []byte) ([]byte, bool, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, false, err
	}
	stream, requested := m["stream"]
	if !requested || stream == false {
		return raw, false, nil
	}
	if stream != true {
		return nil, false, fail("UNSUPPORTED_STREAM", 400)
	}
	m["stream"] = false
	normalized, err := normalize(canonical(m))
	if err != nil {
		return nil, false, err
	}
	return canonical(normalized), true, nil
}
