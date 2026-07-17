package assets

import (
	"encoding/binary"
	"fmt"
)

// box describes one ISOBMFF box: typ is its 4-character type, start is its
// offset in the original data, headerLen is 8 (or 16 when a 64-bit largesize
// is present), and size is the box's full size including its header.
type box struct {
	typ       string
	start     int
	headerLen int
	size      int
}

// walkBoxes parses the sibling boxes in data[start:end], returning them in
// order. It supports both 32-bit sizes and, when size == 1, a following
// 64-bit largesize; size == 0 means "extends to end" (i.e. to end). Every
// field read is bounds-checked against end and len(data); malformed or
// truncated input returns an error rather than panicking.
func walkBoxes(data []byte, start, end int) ([]box, error) {
	if start < 0 || end < start || end > len(data) {
		return nil, fmt.Errorf("isobmff: invalid range [%d,%d) for %d-byte buffer", start, end, len(data))
	}

	var boxes []box
	i := start
	for i < end {
		if i+8 > end {
			return nil, fmt.Errorf("isobmff: truncated box header at offset %d", i)
		}
		size32 := binary.BigEndian.Uint32(data[i : i+4])
		typ := string(data[i+4 : i+8])

		headerLen := 8
		var size int
		switch size32 {
		case 0:
			size = end - i
		case 1:
			if i+16 > end {
				return nil, fmt.Errorf("isobmff: box %q truncated largesize at offset %d", typ, i)
			}
			largesize := binary.BigEndian.Uint64(data[i+8 : i+16])
			if largesize > uint64(end-i) {
				return nil, fmt.Errorf("isobmff: box %q largesize %d overruns buffer", typ, largesize)
			}
			size = int(largesize)
			headerLen = 16
		default:
			size = int(size32)
		}

		boxEnd := i + size
		if size < headerLen || boxEnd < i || boxEnd > end {
			return nil, fmt.Errorf("isobmff: box %q overruns buffer", typ)
		}

		boxes = append(boxes, box{typ: typ, start: i, headerLen: headerLen, size: size})
		i = boxEnd
	}

	return boxes, nil
}

// findChild returns the first child box of the given type among boxes, or
// false if none is present.
func findChild(boxes []box, typ string) (box, bool) {
	for _, b := range boxes {
		if b.typ == typ {
			return b, true
		}
	}
	return box{}, false
}

// readCString reads a null-terminated string starting at pos, not reading
// past limit (the enclosing box's own end) or len(data). It returns the
// string (excluding the terminator) and the position just past the
// terminator.
func readCString(data []byte, pos, limit int) (string, int, error) {
	if pos < 0 || pos > limit || limit > len(data) {
		return "", 0, fmt.Errorf("isobmff: invalid string bounds at offset %d", pos)
	}
	end := pos
	for end < limit && data[end] != 0 {
		end++
	}
	if end >= limit {
		return "", 0, fmt.Errorf("isobmff: unterminated string starting at offset %d", pos)
	}
	return string(data[pos:end]), end + 1, nil
}

// findAVIFMetadataItems locates the meta box (via a top-level walkBoxes),
// then its iinf child, and returns the set of item_IDs whose infe entry
// declares item_type "Exif" or a "mime" item_type with content_type
// "application/rdf+xml" (XMP). A missing meta or iinf box is treated as "no
// metadata items" rather than an error, since a bare AV1 payload with no
// metadata box is not itself malformed. Any short/inconsistent read within a
// present meta/iinf/infe chain is reported as an error.
func findAVIFMetadataItems(data []byte) (map[uint32]bool, error) {
	ids := map[uint32]bool{}

	top, err := walkBoxes(data, 0, len(data))
	if err != nil {
		return nil, fmt.Errorf("avif: walking top-level boxes: %w", err)
	}

	meta, ok := findChild(top, "meta")
	if !ok {
		return ids, nil
	}

	// meta is a FullBox: 4 bytes of version/flags precede its children.
	metaChildStart := meta.start + meta.headerLen + 4
	metaEnd := meta.start + meta.size
	if metaChildStart > metaEnd || metaEnd > len(data) {
		return nil, fmt.Errorf("avif: meta box too short for FullBox header")
	}

	metaChildren, err := walkBoxes(data, metaChildStart, metaEnd)
	if err != nil {
		return nil, fmt.Errorf("avif: walking meta children: %w", err)
	}

	iinf, ok := findChild(metaChildren, "iinf")
	if !ok {
		return ids, nil
	}

	// iinf is a FullBox: version/flags(4), then entry_count (2 bytes for
	// version 0, else 4), then the infe children.
	if iinf.start+iinf.headerLen+4 > len(data) {
		return nil, fmt.Errorf("avif: iinf box too short for FullBox header")
	}
	iinfVersion := data[iinf.start+iinf.headerLen]
	entryCountSize := 2
	if iinfVersion != 0 {
		entryCountSize = 4
	}

	infeStart := iinf.start + iinf.headerLen + 4 + entryCountSize
	infeEnd := iinf.start + iinf.size
	if infeStart > infeEnd || infeEnd > len(data) {
		return nil, fmt.Errorf("avif: iinf box too short for entry_count")
	}

	infeBoxes, err := walkBoxes(data, infeStart, infeEnd)
	if err != nil {
		return nil, fmt.Errorf("avif: walking iinf children: %w", err)
	}

	for _, b := range infeBoxes {
		if b.typ != "infe" {
			continue
		}
		id, itemType, contentType, err := parseInfe(data, b)
		if err != nil {
			return nil, fmt.Errorf("avif: parsing infe at offset %d: %w", b.start, err)
		}
		switch {
		case itemType == "Exif":
			ids[id] = true
		case itemType == "mime" && contentType == "application/rdf+xml":
			ids[id] = true
		}
	}

	return ids, nil
}

// parseInfe parses an ItemInfoEntry (infe) FullBox, supporting version 2
// (16-bit item_ID) and version 3 (32-bit item_ID) — the only versions the
// AVIF/MIAF profile emits. It returns the item_ID, the 4-character
// item_type, and (when item_type == "mime") the content_type string.
func parseInfe(data []byte, b box) (id uint32, itemType, contentType string, err error) {
	boxEnd := b.start + b.size
	pos := b.start + b.headerLen

	if pos+4 > boxEnd {
		return 0, "", "", fmt.Errorf("truncated FullBox header")
	}
	version := data[pos]
	pos += 4 // version(1) + flags(3)

	var idSize int
	switch version {
	case 2:
		idSize = 2
	case 3:
		idSize = 4
	default:
		return 0, "", "", fmt.Errorf("unsupported infe version %d", version)
	}

	if pos+idSize > boxEnd {
		return 0, "", "", fmt.Errorf("truncated item_ID")
	}
	if idSize == 2 {
		id = uint32(binary.BigEndian.Uint16(data[pos : pos+idSize]))
	} else {
		id = binary.BigEndian.Uint32(data[pos : pos+idSize])
	}
	pos += idSize

	pos += 2 // item_protection_index
	if pos > boxEnd {
		return 0, "", "", fmt.Errorf("truncated item_protection_index")
	}

	if pos+4 > boxEnd {
		return 0, "", "", fmt.Errorf("truncated item_type")
	}
	itemType = string(data[pos : pos+4])
	pos += 4

	// item_name (null-terminated) always follows item_type in versions 2/3.
	_, pos, err = readCString(data, pos, boxEnd)
	if err != nil {
		return 0, "", "", fmt.Errorf("item_name: %w", err)
	}

	if itemType == "mime" {
		contentType, _, err = readCString(data, pos, boxEnd)
		if err != nil {
			return 0, "", "", fmt.Errorf("content_type: %w", err)
		}
	}

	return id, itemType, contentType, nil
}
