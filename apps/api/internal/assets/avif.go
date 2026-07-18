package assets

import (
	"encoding/binary"
	"fmt"
	"sort"
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

// ilocExtent is one (offset, length) extent of an iloc item, plus its
// extent_index (only meaningful for construction_method 2, which stripAVIF
// does not support but must still parse past correctly).
type ilocExtent struct {
	index  uint64
	offset uint64
	length uint64
}

// ilocItem is one parsed iloc entry: an item_ID, its construction_method
// (0 = file/mdat offset, 1 = idat offset, 2 = another item's data, unsupported
// here), its base_offset (added to every extent_offset), and its extents.
type ilocItem struct {
	id                 uint32
	constructionMethod uint8
	dataRefIndex       uint16
	baseOffset         uint64
	extents            []ilocExtent
}

// ilocBox is a fully parsed iloc box: the field-width configuration that
// governs how every item/extent is encoded, plus the parsed items.
type ilocBox struct {
	version        uint8
	offsetSize     int
	lengthSize     int
	baseOffsetSize int
	indexSize      int
	items          []ilocItem
}

// readUintField reads a big-endian unsigned integer of the given byte width
// (0, 4, or 8 — the only widths iloc's nibble-encoded field sizes produce in
// practice) at pos, bounds-checked against limit. A width of 0 means the
// field is absent from the bitstream and its value is implicitly 0.
func readUintField(data []byte, pos, limit, size int) (uint64, int, error) {
	switch size {
	case 0:
		return 0, pos, nil
	case 4:
		if pos+4 > limit {
			return 0, 0, fmt.Errorf("truncated 4-byte field at offset %d", pos)
		}
		return uint64(binary.BigEndian.Uint32(data[pos : pos+4])), pos + 4, nil
	case 8:
		if pos+8 > limit {
			return 0, 0, fmt.Errorf("truncated 8-byte field at offset %d", pos)
		}
		return binary.BigEndian.Uint64(data[pos : pos+8]), pos + 8, nil
	default:
		return 0, 0, fmt.Errorf("unsupported field size %d", size)
	}
}

// appendUintField appends v as a big-endian unsigned integer of the given
// byte width. It errors if v does not fit (including v != 0 for a 0-byte
// field), so a value that has grown too large for its encoded width fails
// closed instead of silently truncating.
func appendUintField(out []byte, v uint64, size int) ([]byte, error) {
	switch size {
	case 0:
		if v != 0 {
			return nil, fmt.Errorf("value %d does not fit in a 0-byte field", v)
		}
		return out, nil
	case 4:
		if v > 0xFFFFFFFF {
			return nil, fmt.Errorf("value %d does not fit in a 4-byte field", v)
		}
		return append(out, appendBE32(v)...), nil
	case 8:
		return append(out, appendBE64(v)...), nil
	default:
		return nil, fmt.Errorf("unsupported field size %d", size)
	}
}

func appendBE16(v uint64) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(v))
	return b
}

func appendBE32(v uint64) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(v))
	return b
}

func appendBE64(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

// packBox wraps body (which, for a FullBox, already includes its own
// version/flags prefix) in a complete box: a 32-bit size + 4-char type header,
// or — only if body would overflow a 32-bit size — a size==1 box with a
// 64-bit largesize.
func packBox(typ string, body []byte) []byte {
	total64 := uint64(8 + len(body))
	if total64 <= 0xFFFFFFFF {
		out := make([]byte, 0, total64)
		out = append(out, appendBE32(total64)...)
		out = append(out, []byte(typ)...)
		out = append(out, body...)
		return out
	}
	largesize := uint64(16 + len(body))
	out := make([]byte, 0, largesize)
	out = append(out, appendBE32(1)...)
	out = append(out, []byte(typ)...)
	out = append(out, appendBE64(largesize)...)
	out = append(out, body...)
	return out
}

// parseIloc parses an ItemLocationBox (iloc) FullBox, supporting versions 0-2
// and any offset_size/length_size/base_offset_size/index_size in {0,4,8} (the
// only widths the 4-bit nibble fields can sensibly hold in practice). Every
// read is bounds-checked against the box's own end and the buffer length.
func parseIloc(data []byte, b box) (*ilocBox, error) {
	boxEnd := b.start + b.size
	pos := b.start + b.headerLen
	if pos+4 > boxEnd {
		return nil, fmt.Errorf("iloc: truncated FullBox header")
	}
	version := data[pos]
	pos += 4

	if pos+2 > boxEnd {
		return nil, fmt.Errorf("iloc: truncated field-size bytes")
	}
	sizesByte0 := data[pos]
	sizesByte1 := data[pos+1]
	pos += 2

	offsetSize := int(sizesByte0 >> 4)
	lengthSize := int(sizesByte0 & 0x0F)
	baseOffsetSize := int(sizesByte1 >> 4)
	indexSize := int(sizesByte1 & 0x0F)

	if offsetSize == 0 || lengthSize == 0 {
		return nil, fmt.Errorf("iloc: unsupported offset_size/length_size 0")
	}
	for _, sz := range []int{offsetSize, lengthSize, baseOffsetSize, indexSize} {
		if sz != 0 && sz != 4 && sz != 8 {
			return nil, fmt.Errorf("iloc: unsupported field size %d", sz)
		}
	}

	var itemCount int
	switch {
	case version < 2:
		if pos+2 > boxEnd {
			return nil, fmt.Errorf("iloc: truncated item_count")
		}
		itemCount = int(binary.BigEndian.Uint16(data[pos : pos+2]))
		pos += 2
	case version == 2:
		if pos+4 > boxEnd {
			return nil, fmt.Errorf("iloc: truncated item_count")
		}
		itemCount = int(binary.BigEndian.Uint32(data[pos : pos+4]))
		pos += 4
	default:
		return nil, fmt.Errorf("iloc: unsupported version %d", version)
	}

	// Bound item_count against the box's remaining bytes before reserving
	// capacity for it: item_count is attacker-controlled (32 bits wide for
	// version 2, up to 0xFFFFFFFF), and make([]ilocItem, 0, itemCount) below
	// would otherwise allocate on that count before any per-item bounds check
	// runs, letting a tiny hostile box request hundreds of gigabytes. Every
	// item needs at least: item_ID (2 or 4 bytes) + construction_method (2
	// bytes, versions 1/2 only) + data_reference_index (2 bytes) +
	// base_offset (baseOffsetSize bytes) + extent_count (2 bytes).
	minItemBytes := 2 + 2 // data_reference_index + extent_count
	if version < 2 {
		minItemBytes += 2 // item_ID (16-bit)
	} else {
		minItemBytes += 4 // item_ID (32-bit)
	}
	if version == 1 || version == 2 {
		minItemBytes += 2 // construction_method
	}
	minItemBytes += baseOffsetSize
	if itemCount > (boxEnd-pos)/minItemBytes {
		return nil, fmt.Errorf("iloc: item_count %d exceeds box size", itemCount)
	}

	items := make([]ilocItem, 0, itemCount)
	for i := 0; i < itemCount; i++ {
		var id uint32
		if version < 2 {
			if pos+2 > boxEnd {
				return nil, fmt.Errorf("iloc: truncated item_ID")
			}
			id = uint32(binary.BigEndian.Uint16(data[pos : pos+2]))
			pos += 2
		} else {
			if pos+4 > boxEnd {
				return nil, fmt.Errorf("iloc: truncated item_ID")
			}
			id = binary.BigEndian.Uint32(data[pos : pos+4])
			pos += 4
		}

		var constructionMethod uint8
		if version == 1 || version == 2 {
			if pos+2 > boxEnd {
				return nil, fmt.Errorf("iloc: truncated construction_method")
			}
			constructionMethod = uint8(binary.BigEndian.Uint16(data[pos:pos+2]) & 0x0F)
			pos += 2
		}

		if pos+2 > boxEnd {
			return nil, fmt.Errorf("iloc: truncated data_reference_index")
		}
		dataRefIndex := binary.BigEndian.Uint16(data[pos : pos+2])
		pos += 2

		baseOffset, newPos, err := readUintField(data, pos, boxEnd, baseOffsetSize)
		if err != nil {
			return nil, fmt.Errorf("iloc: base_offset: %w", err)
		}
		pos = newPos

		if pos+2 > boxEnd {
			return nil, fmt.Errorf("iloc: truncated extent_count")
		}
		extentCount := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		pos += 2

		// Same reasoning as the item_count guard above: bound extent_count
		// against the box's remaining bytes before reserving capacity for it.
		// extent_count is only 16 bits so this isn't independently a GB-scale
		// vector, but it fails closed before allocating instead of relying on
		// the per-field bounds checks in the loop below to catch it late.
		minExtentBytes := offsetSize + lengthSize
		if (version == 1 || version == 2) && indexSize > 0 {
			minExtentBytes += indexSize
		}
		if extentCount > (boxEnd-pos)/minExtentBytes {
			return nil, fmt.Errorf("iloc: extent_count %d exceeds box size", extentCount)
		}

		extents := make([]ilocExtent, 0, extentCount)
		for j := 0; j < extentCount; j++ {
			var idx uint64
			if (version == 1 || version == 2) && indexSize > 0 {
				idx, pos, err = readUintField(data, pos, boxEnd, indexSize)
				if err != nil {
					return nil, fmt.Errorf("iloc: extent_index: %w", err)
				}
			}
			off, newPos2, err := readUintField(data, pos, boxEnd, offsetSize)
			if err != nil {
				return nil, fmt.Errorf("iloc: extent_offset: %w", err)
			}
			pos = newPos2
			length, newPos3, err := readUintField(data, pos, boxEnd, lengthSize)
			if err != nil {
				return nil, fmt.Errorf("iloc: extent_length: %w", err)
			}
			pos = newPos3
			extents = append(extents, ilocExtent{index: idx, offset: off, length: length})
		}

		items = append(items, ilocItem{
			id:                 id,
			constructionMethod: constructionMethod,
			dataRefIndex:       dataRefIndex,
			baseOffset:         baseOffset,
			extents:            extents,
		})
	}

	if pos > boxEnd {
		return nil, fmt.Errorf("iloc: entries overran box")
	}

	return &ilocBox{
		version:        version,
		offsetSize:     offsetSize,
		lengthSize:     lengthSize,
		baseOffsetSize: baseOffsetSize,
		indexSize:      indexSize,
		items:          items,
	}, nil
}

// buildIlocBytes serializes items back into a complete iloc box, reusing
// orig's version and field widths (offset_size/length_size/base_offset_size/
// index_size never change — only which items survive and their offsets do).
func buildIlocBytes(data []byte, origBox box, orig *ilocBox, items []ilocItem) ([]byte, error) {
	verFlags := data[origBox.start+origBox.headerLen : origBox.start+origBox.headerLen+4]

	rest := make([]byte, 0, 2+4+len(items)*16)
	rest = append(rest, byte(orig.offsetSize<<4|orig.lengthSize), byte(orig.baseOffsetSize<<4|orig.indexSize))

	if len(items) > 0xFFFFFFFF {
		return nil, fmt.Errorf("iloc: too many items")
	}
	if orig.version < 2 {
		if len(items) > 0xFFFF {
			return nil, fmt.Errorf("iloc: too many items for version %d", orig.version)
		}
		rest = append(rest, appendBE16(uint64(len(items)))...)
	} else {
		rest = append(rest, appendBE32(uint64(len(items)))...)
	}

	for _, it := range items {
		if orig.version < 2 {
			if it.id > 0xFFFF {
				return nil, fmt.Errorf("iloc: item_ID %d does not fit version %d", it.id, orig.version)
			}
			rest = append(rest, appendBE16(uint64(it.id))...)
		} else {
			rest = append(rest, appendBE32(uint64(it.id))...)
		}
		if orig.version == 1 || orig.version == 2 {
			rest = append(rest, appendBE16(uint64(it.constructionMethod&0x0F))...)
		}
		rest = append(rest, appendBE16(uint64(it.dataRefIndex))...)

		var err error
		rest, err = appendUintField(rest, it.baseOffset, orig.baseOffsetSize)
		if err != nil {
			return nil, fmt.Errorf("iloc: item %d base_offset: %w", it.id, err)
		}

		if len(it.extents) > 0xFFFF {
			return nil, fmt.Errorf("iloc: item %d has too many extents", it.id)
		}
		rest = append(rest, appendBE16(uint64(len(it.extents)))...)

		for _, ext := range it.extents {
			if (orig.version == 1 || orig.version == 2) && orig.indexSize > 0 {
				rest, err = appendUintField(rest, ext.index, orig.indexSize)
				if err != nil {
					return nil, fmt.Errorf("iloc: item %d extent_index: %w", it.id, err)
				}
			}
			rest, err = appendUintField(rest, ext.offset, orig.offsetSize)
			if err != nil {
				return nil, fmt.Errorf("iloc: item %d extent_offset: %w", it.id, err)
			}
			rest, err = appendUintField(rest, ext.length, orig.lengthSize)
			if err != nil {
				return nil, fmt.Errorf("iloc: item %d extent_length: %w", it.id, err)
			}
		}
	}

	body := make([]byte, 0, 4+len(rest))
	body = append(body, verFlags...)
	body = append(body, rest...)
	return packBox("iloc", body), nil
}

// buildIinf rebuilds an iinf box, dropping the infe entry for every removed
// item ID and copying every surviving infe (and any non-infe child) verbatim,
// then rewriting entry_count.
func buildIinf(data []byte, iinfB box, infeBoxes []box, removedIDs map[uint32]bool) ([]byte, error) {
	verFlags := data[iinfB.start+iinfB.headerLen : iinfB.start+iinfB.headerLen+4]
	version := verFlags[0]
	entryCountSize := 2
	if version != 0 {
		entryCountSize = 4
	}

	var kept []byte
	count := 0
	for _, c := range infeBoxes {
		if c.typ != "infe" {
			kept = append(kept, data[c.start:c.start+c.size]...)
			continue
		}
		id, _, _, err := parseInfe(data, c)
		if err != nil {
			return nil, fmt.Errorf("rebuilding iinf: parsing infe at offset %d: %w", c.start, err)
		}
		if removedIDs[id] {
			continue
		}
		kept = append(kept, data[c.start:c.start+c.size]...)
		count++
	}

	body := make([]byte, 0, 4+entryCountSize+len(kept))
	body = append(body, verFlags...)
	if entryCountSize == 2 {
		if count > 0xFFFF {
			return nil, fmt.Errorf("iinf: too many items for version %d", version)
		}
		body = append(body, appendBE16(uint64(count))...)
	} else {
		body = append(body, appendBE32(uint64(count))...)
	}
	body = append(body, kept...)
	return packBox("iinf", body), nil
}

// rebuildIref rebuilds an iref box: any SingleItemTypeReferenceBox whose
// from_item_ID is a removed item is dropped entirely; otherwise, any
// to_item_ID referencing a removed item is dropped from that box's list, and
// the box itself is dropped if that leaves it with no references.
func rebuildIref(data []byte, irefB box, removedIDs map[uint32]bool) ([]byte, error) {
	boxEnd := irefB.start + irefB.size
	if irefB.start+irefB.headerLen+4 > boxEnd {
		return nil, fmt.Errorf("iref: too short for FullBox header")
	}
	verFlags := data[irefB.start+irefB.headerLen : irefB.start+irefB.headerLen+4]
	version := verFlags[0]
	idSize := 2
	if version != 0 {
		idSize = 4
	}

	childStart := irefB.start + irefB.headerLen + 4
	children, err := walkBoxes(data, childStart, boxEnd)
	if err != nil {
		return nil, fmt.Errorf("iref: walking children: %w", err)
	}

	var kept []byte
	for _, c := range children {
		bodyStart := c.start + c.headerLen
		bodyEnd := c.start + c.size
		if bodyStart+idSize+2 > bodyEnd {
			return nil, fmt.Errorf("iref: child %q too short", c.typ)
		}
		var fromID uint32
		if idSize == 2 {
			fromID = uint32(binary.BigEndian.Uint16(data[bodyStart : bodyStart+2]))
		} else {
			fromID = binary.BigEndian.Uint32(data[bodyStart : bodyStart+4])
		}
		if removedIDs[fromID] {
			continue
		}

		countPos := bodyStart + idSize
		count := int(binary.BigEndian.Uint16(data[countPos : countPos+2]))
		toStart := countPos + 2

		var newTo []byte
		keptCount := 0
		for i := 0; i < count; i++ {
			off := toStart + i*idSize
			if off+idSize > bodyEnd {
				return nil, fmt.Errorf("iref: child %q truncated to_item_ID", c.typ)
			}
			var toID uint32
			if idSize == 2 {
				toID = uint32(binary.BigEndian.Uint16(data[off : off+idSize]))
			} else {
				toID = binary.BigEndian.Uint32(data[off : off+idSize])
			}
			if removedIDs[toID] {
				continue
			}
			newTo = append(newTo, data[off:off+idSize]...)
			keptCount++
		}
		if keptCount == 0 {
			continue
		}

		newBody := make([]byte, 0, idSize+2+len(newTo))
		newBody = append(newBody, data[bodyStart:bodyStart+idSize]...)
		newBody = append(newBody, appendBE16(uint64(keptCount))...)
		newBody = append(newBody, newTo...)
		kept = append(kept, packBox(c.typ, newBody)...)
	}

	body := make([]byte, 0, 4+len(kept))
	body = append(body, verFlags...)
	body = append(body, kept...)
	return packBox("iref", body), nil
}

// rebuildIpma rebuilds one ipma (ItemPropertyAssociation) box, dropping the
// association entry for every removed item ID and copying every surviving
// entry verbatim (each entry is byte-aligned: item_ID + association_count +
// association_count 1-or-2-byte records), then rewriting entry_count.
func rebuildIpma(data []byte, ipmaB box, removedIDs map[uint32]bool) ([]byte, error) {
	boxEnd := ipmaB.start + ipmaB.size
	pos := ipmaB.start + ipmaB.headerLen
	if pos+4 > boxEnd {
		return nil, fmt.Errorf("ipma: truncated FullBox header")
	}
	verFlags := data[pos : pos+4]
	version := verFlags[0]
	flags := uint32(verFlags[1])<<16 | uint32(verFlags[2])<<8 | uint32(verFlags[3])
	pos += 4

	if pos+4 > boxEnd {
		return nil, fmt.Errorf("ipma: truncated entry_count")
	}
	entryCount := binary.BigEndian.Uint32(data[pos : pos+4])
	pos += 4

	idSize := 2
	if version >= 1 {
		idSize = 4
	}
	assocSize := 1
	if flags&1 != 0 {
		assocSize = 2
	}

	var kept []byte
	keptCount := uint32(0)
	for i := uint32(0); i < entryCount; i++ {
		entryStart := pos
		if pos+idSize > boxEnd {
			return nil, fmt.Errorf("ipma: truncated item_ID")
		}
		var id uint32
		if idSize == 2 {
			id = uint32(binary.BigEndian.Uint16(data[pos : pos+idSize]))
		} else {
			id = binary.BigEndian.Uint32(data[pos : pos+idSize])
		}
		pos += idSize

		if pos+1 > boxEnd {
			return nil, fmt.Errorf("ipma: truncated association_count")
		}
		assocCount := int(data[pos])
		pos++

		need := assocCount * assocSize
		if pos+need > boxEnd {
			return nil, fmt.Errorf("ipma: truncated associations")
		}
		pos += need

		if removedIDs[id] {
			continue
		}
		kept = append(kept, data[entryStart:pos]...)
		keptCount++
	}
	if pos > boxEnd {
		return nil, fmt.Errorf("ipma: entries overran box")
	}

	body := make([]byte, 0, 8+len(kept))
	body = append(body, verFlags...)
	body = append(body, appendBE32(uint64(keptCount))...)
	body = append(body, kept...)
	return packBox("ipma", body), nil
}

// rebuildIprp rebuilds an iprp (ItemPropertiesBox) box: ipco (the property
// container) is copied verbatim — image properties are never touched — while
// every ipma child is rebuilt via rebuildIpma.
func rebuildIprp(data []byte, iprpB box, removedIDs map[uint32]bool) ([]byte, error) {
	childStart := iprpB.start + iprpB.headerLen // iprp is a plain Box, no FullBox prefix.
	childEnd := iprpB.start + iprpB.size
	children, err := walkBoxes(data, childStart, childEnd)
	if err != nil {
		return nil, fmt.Errorf("iprp: walking children: %w", err)
	}

	var out []byte
	for _, c := range children {
		if c.typ != "ipma" {
			out = append(out, data[c.start:c.start+c.size]...)
			continue
		}
		newIpma, err := rebuildIpma(data, c, removedIDs)
		if err != nil {
			return nil, fmt.Errorf("iprp: %w", err)
		}
		out = append(out, newIpma...)
	}
	return packBox("iprp", out), nil
}

// byteRange is a half-open [start, end) span of relative offsets within a
// payload (mdat's or idat's), used to describe bytes being excised.
type byteRange struct {
	start, end int
}

// validateRanges sorts ranges by start and rejects any that overlap — two
// removed items whose extents overlap indicate a layout stripAVIF doesn't
// understand, so it fails closed rather than guessing.
func validateRanges(ranges []byteRange) ([]byteRange, error) {
	sorted := append([]byteRange(nil), ranges...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].start < sorted[j].start })
	for i := 1; i < len(sorted); i++ {
		if sorted[i].start < sorted[i-1].end {
			return nil, fmt.Errorf("overlapping removed ranges [%d,%d) and [%d,%d)", sorted[i-1].start, sorted[i-1].end, sorted[i].start, sorted[i].end)
		}
	}
	return sorted, nil
}

// excise removes the given non-overlapping, sorted ranges from payload,
// returning the shortened bytes and a mapper from an old offset (into
// payload) to its new offset in the returned bytes. The mapper's second
// return value is false when the queried offset falls inside a removed
// range (i.e. there is no valid new position for it).
func excise(payload []byte, removed []byteRange) ([]byte, func(int) (int, bool)) {
	type seg struct{ start, end, shift int }
	var segs []seg
	var out []byte
	pos := 0
	cumRemoved := 0
	for _, r := range removed {
		if r.start > pos {
			segs = append(segs, seg{pos, r.start, cumRemoved})
			out = append(out, payload[pos:r.start]...)
		}
		cumRemoved += r.end - r.start
		pos = r.end
	}
	if pos < len(payload) {
		segs = append(segs, seg{pos, len(payload), cumRemoved})
		out = append(out, payload[pos:]...)
	}

	mapper := func(off int) (int, bool) {
		for _, s := range segs {
			if off >= s.start && off <= s.end {
				return off - s.shift, true
			}
		}
		if off == len(payload) {
			return off - cumRemoved, true
		}
		return 0, false
	}
	return out, mapper
}

// stripAVIF removes every Exif/XMP metadata item from an AVIF file and
// rewrites the ISOBMFF tables/offsets so the result parses as a valid,
// smaller AVIF whose av01 (and any other surviving) item payloads are
// byte-identical to the input's — the strip is lossless for pixel data.
//
// If the file has no meta box, or has one but no Exif/XMP items, the result
// is an independent copy of data, byte-identical to it in the latter case
// (which also makes repeated calls idempotent). Any malformed or
// unsupported-layout input returns an error rather than a corrupt file.
func stripAVIF(data []byte) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out = nil
			err = fmt.Errorf("avif: internal error while stripping: %v", r)
		}
	}()

	top, err := walkBoxes(data, 0, len(data))
	if err != nil {
		return nil, fmt.Errorf("avif: walking top-level boxes: %w", err)
	}
	metaBox, ok := findChild(top, "meta")
	if !ok {
		return append([]byte(nil), data...), nil
	}

	ids, err := findAVIFMetadataItems(data)
	if err != nil {
		return nil, fmt.Errorf("avif: finding metadata items: %w", err)
	}
	if len(ids) == 0 {
		return append([]byte(nil), data...), nil
	}

	return rebuildAVIF(data, top, metaBox, ids)
}

// rebuildAVIF performs the actual rewrite once stripAVIF has determined there
// is at least one metadata item to remove. See the package-level algorithm
// notes on stripAVIF for the overall approach.
func rebuildAVIF(data []byte, top []box, metaBox box, removedIDs map[uint32]bool) ([]byte, error) {
	metaChildStart := metaBox.start + metaBox.headerLen + 4
	metaEnd := metaBox.start + metaBox.size
	if metaChildStart > metaEnd || metaEnd > len(data) {
		return nil, fmt.Errorf("avif: meta box too short for FullBox header")
	}

	metaChildren, err := walkBoxes(data, metaChildStart, metaEnd)
	if err != nil {
		return nil, fmt.Errorf("avif: walking meta children: %w", err)
	}

	iinfB, ok := findChild(metaChildren, "iinf")
	if !ok {
		return nil, fmt.Errorf("avif: metadata items present but no iinf box")
	}
	ilocB, ok := findChild(metaChildren, "iloc")
	if !ok {
		return nil, fmt.Errorf("avif: metadata items present but no iloc box")
	}
	infeBoxes, err := walkBoxes(data, iinfB.start+iinfB.headerLen+4+iinfEntryCountSize(data, iinfB), iinfB.start+iinfB.size)
	if err != nil {
		return nil, fmt.Errorf("avif: walking iinf children: %w", err)
	}

	parsedIloc, err := parseIloc(data, ilocB)
	if err != nil {
		return nil, fmt.Errorf("avif: parsing iloc: %w", err)
	}

	mdatB, hasMdat := findChild(top, "mdat")
	idatB, hasIdat := findChild(metaChildren, "idat")
	iprpB, hasIprp := findChild(metaChildren, "iprp")
	irefB, hasIref := findChild(metaChildren, "iref")

	var mdatPayloadStart, mdatPayloadLen int
	if hasMdat {
		mdatPayloadStart = mdatB.start + mdatB.headerLen
		mdatPayloadLen = mdatB.size - mdatB.headerLen
	}
	var idatPayloadStart, idatPayloadLen int
	if hasIdat {
		idatPayloadStart = idatB.start + idatB.headerLen
		idatPayloadLen = idatB.size - idatB.headerLen
	}

	// Fail closed up front on any item/extent layout this function can't
	// safely rewrite, whether or not that item is being removed.
	for _, it := range parsedIloc.items {
		switch it.constructionMethod {
		case 0:
			if !hasMdat {
				return nil, fmt.Errorf("avif: item %d uses construction_method 0 but file has no mdat box", it.id)
			}
		case 1:
			if !hasIdat {
				return nil, fmt.Errorf("avif: item %d uses construction_method 1 but meta has no idat box", it.id)
			}
		default:
			return nil, fmt.Errorf("avif: item %d uses unsupported construction_method %d", it.id, it.constructionMethod)
		}
	}

	var mdatRemoved, idatRemoved []byteRange
	for _, it := range parsedIloc.items {
		if !removedIDs[it.id] {
			continue
		}
		for _, ext := range it.extents {
			abs := it.baseOffset + ext.offset
			if abs > uint64(len(data)) || ext.length > uint64(len(data)) {
				return nil, fmt.Errorf("avif: item %d extent out of range", it.id)
			}
			switch it.constructionMethod {
			case 0:
				start := int(abs) - mdatPayloadStart
				end := start + int(ext.length)
				if start < 0 || end < start || end > mdatPayloadLen {
					return nil, fmt.Errorf("avif: item %d mdat extent [%d,%d) out of bounds", it.id, start, end)
				}
				mdatRemoved = append(mdatRemoved, byteRange{start, end})
			case 1:
				start := int(abs)
				end := start + int(ext.length)
				if start < 0 || end < start || end > idatPayloadLen {
					return nil, fmt.Errorf("avif: item %d idat extent [%d,%d) out of bounds", it.id, start, end)
				}
				idatRemoved = append(idatRemoved, byteRange{start, end})
			}
		}
	}

	mdatRemovedSorted, err := validateRanges(mdatRemoved)
	if err != nil {
		return nil, fmt.Errorf("avif: mdat removal: %w", err)
	}
	idatRemovedSorted, err := validateRanges(idatRemoved)
	if err != nil {
		return nil, fmt.Errorf("avif: idat removal: %w", err)
	}

	var newMdatPayload []byte
	var mdatMapper func(int) (int, bool)
	if hasMdat {
		newMdatPayload, mdatMapper = excise(data[mdatPayloadStart:mdatPayloadStart+mdatPayloadLen], mdatRemovedSorted)
	}
	var newIdatPayload []byte
	var idatMapper func(int) (int, bool)
	if hasIdat {
		newIdatPayload, idatMapper = excise(data[idatPayloadStart:idatPayloadStart+idatPayloadLen], idatRemovedSorted)
	}

	// pendingMdatOffset records, per surviving item/extent, the position
	// still to be resolved once the new mdat's absolute file offset is known
	// (its value only depends on box structure, computed below).
	type pendingMdatOffset struct {
		itemIdx, extentIdx int
		relInNewMdat       int
	}
	var pending []pendingMdatOffset

	survivingItems := make([]ilocItem, 0, len(parsedIloc.items))
	for _, it := range parsedIloc.items {
		if removedIDs[it.id] {
			continue
		}
		newExtents := make([]ilocExtent, len(it.extents))
		for i, ext := range it.extents {
			abs := it.baseOffset + ext.offset
			switch it.constructionMethod {
			case 0:
				rel := int(abs) - mdatPayloadStart
				newRel, mapped := mdatMapper(rel)
				if !mapped {
					return nil, fmt.Errorf("avif: surviving item %d references a removed mdat range", it.id)
				}
				pending = append(pending, pendingMdatOffset{itemIdx: len(survivingItems), extentIdx: i, relInNewMdat: newRel})
				newExtents[i] = ilocExtent{index: ext.index, offset: 0, length: ext.length}
			case 1:
				newRel, mapped := idatMapper(int(abs))
				if !mapped {
					return nil, fmt.Errorf("avif: surviving item %d references a removed idat range", it.id)
				}
				if uint64(newRel) < it.baseOffset {
					return nil, fmt.Errorf("avif: item %d idat offset underflows base_offset", it.id)
				}
				newExtents[i] = ilocExtent{index: ext.index, offset: uint64(newRel) - it.baseOffset, length: ext.length}
			}
		}
		newItem := it
		newItem.extents = newExtents
		survivingItems = append(survivingItems, newItem)
	}

	newIinf, err := buildIinf(data, iinfB, infeBoxes, removedIDs)
	if err != nil {
		return nil, fmt.Errorf("avif: rebuilding iinf: %w", err)
	}

	var newIdatBytes []byte
	if hasIdat {
		newIdatBytes = packBox("idat", newIdatPayload)
	}
	var newIprpBytes []byte
	if hasIprp {
		newIprpBytes, err = rebuildIprp(data, iprpB, removedIDs)
		if err != nil {
			return nil, fmt.Errorf("avif: rebuilding iprp: %w", err)
		}
	}
	var newIrefBytes []byte
	if hasIref {
		newIrefBytes, err = rebuildIref(data, irefB, removedIDs)
		if err != nil {
			return nil, fmt.Errorf("avif: rebuilding iref: %w", err)
		}
	}

	buildMeta := func(ilocBytes []byte) []byte {
		var children []byte
		for _, c := range metaChildren {
			switch {
			case c.typ == "iinf":
				children = append(children, newIinf...)
			case c.typ == "iloc":
				children = append(children, ilocBytes...)
			case hasIdat && c.start == idatB.start:
				children = append(children, newIdatBytes...)
			case hasIprp && c.start == iprpB.start:
				children = append(children, newIprpBytes...)
			case hasIref && c.start == irefB.start:
				children = append(children, newIrefBytes...)
			default:
				children = append(children, data[c.start:c.start+c.size]...)
			}
		}
		body := make([]byte, 0, 4+len(children))
		body = append(body, data[metaBox.start+metaBox.headerLen:metaChildStart]...)
		body = append(body, children...)
		return packBox("meta", body)
	}

	// Pass 1 (draft): every item/extent's structure (byte widths, counts) is
	// already final; only pending offset VALUES for mdat items are not, and a
	// field's encoded width never depends on its value. So a draft iloc/meta
	// built with placeholder 0 offsets has exactly the final byte length,
	// letting us locate the new mdat's position before its real offsets are
	// known.
	draftIloc, err := buildIlocBytes(data, ilocB, parsedIloc, survivingItems)
	if err != nil {
		return nil, fmt.Errorf("avif: building draft iloc: %w", err)
	}
	draftMeta := buildMeta(draftIloc)

	prefixLen := 0
	for _, b := range top {
		if hasMdat && b.start == mdatB.start {
			break
		}
		if b.start == metaBox.start {
			prefixLen += len(draftMeta)
		} else {
			prefixLen += b.size
		}
	}
	mdatHeaderLenNew := 8
	if uint64(8+len(newMdatPayload)) > 0xFFFFFFFF {
		mdatHeaderLenNew = 16
	}
	mdatPayloadStartNew := prefixLen + mdatHeaderLenNew

	// Pass 2 (final): resolve every pending mdat-relative position into its
	// real absolute file offset now that the new mdat's position is known.
	for _, p := range pending {
		it := survivingItems[p.itemIdx]
		final := uint64(mdatPayloadStartNew+p.relInNewMdat) - it.baseOffset
		if uint64(mdatPayloadStartNew+p.relInNewMdat) < it.baseOffset {
			return nil, fmt.Errorf("avif: item %d mdat offset underflows base_offset", it.id)
		}
		survivingItems[p.itemIdx].extents[p.extentIdx].offset = final
	}

	finalIloc, err := buildIlocBytes(data, ilocB, parsedIloc, survivingItems)
	if err != nil {
		return nil, fmt.Errorf("avif: building final iloc: %w", err)
	}
	if len(finalIloc) != len(draftIloc) {
		return nil, fmt.Errorf("avif: internal error: final iloc length %d != draft length %d", len(finalIloc), len(draftIloc))
	}
	finalMeta := buildMeta(finalIloc)

	var finalMdat []byte
	if hasMdat {
		finalMdat = packBox("mdat", newMdatPayload)
	}

	var out []byte
	for _, b := range top {
		switch {
		case b.start == metaBox.start:
			out = append(out, finalMeta...)
		case hasMdat && b.start == mdatB.start:
			out = append(out, finalMdat...)
		default:
			out = append(out, data[b.start:b.start+b.size]...)
		}
	}
	return out, nil
}

// iinfEntryCountSize returns the byte width of iinf's entry_count field: 2
// for version 0, 4 otherwise. It bounds-checks the version byte itself.
func iinfEntryCountSize(data []byte, iinfB box) int {
	if iinfB.start+iinfB.headerLen >= len(data) {
		return 2
	}
	if data[iinfB.start+iinfB.headerLen] != 0 {
		return 4
	}
	return 2
}
