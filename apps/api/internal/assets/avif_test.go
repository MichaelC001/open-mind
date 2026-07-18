package assets

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// --- byte-assembly helpers shared by the fixture builder ---

func be16(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

func be32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

func be64(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

// mkBox prepends a 32-bit size + 4-char type to body, forming a complete box.
func mkBox(typ string, body []byte) []byte {
	out := make([]byte, 0, 8+len(body))
	out = append(out, be32(uint32(8+len(body)))...)
	out = append(out, []byte(typ)...)
	out = append(out, body...)
	return out
}

// fullBoxBody prepends the 1-byte version + 3-byte flags (always zero here)
// that every FullBox carries ahead of its own fields.
func fullBoxBody(version byte, rest []byte) []byte {
	body := make([]byte, 0, 4+len(rest))
	body = append(body, version, 0, 0, 0)
	body = append(body, rest...)
	return body
}

// --- synthetic AVIF fixture builder ---
//
// testItem describes one meta-item to embed in a fixture: its item_ID, its
// item_type (e.g. "av01", "Exif", "mime"), its content_type (only meaningful
// when itemType == "mime"), and the payload bytes it owns in mdat.
type testItem struct {
	id          uint32
	itemType    string
	contentType string
	data        []byte
}

func infeBox(it testItem) []byte {
	rest := make([]byte, 0, 2+2+4+1+len(it.contentType)+1)
	rest = append(rest, be16(uint16(it.id))...) // item_ID (version 2: 16-bit)
	rest = append(rest, be16(0)...)             // item_protection_index
	rest = append(rest, []byte(it.itemType)...) // item_type
	rest = append(rest, 0x00)                   // item_name (empty, null-terminated)
	if it.itemType == "mime" {
		rest = append(rest, []byte(it.contentType)...)
		rest = append(rest, 0x00) // content_type, null-terminated
	}
	return mkBox("infe", fullBoxBody(2, rest)) // infe version 2
}

func iinfBox(items []testItem) []byte {
	rest := be16(uint16(len(items))) // entry_count (iinf version 0: 16-bit)
	for _, it := range items {
		rest = append(rest, infeBox(it)...)
	}
	return mkBox("iinf", fullBoxBody(0, rest))
}

// singleItemReferenceBox builds one SingleItemTypeReferenceBox child of an
// iref box: refType (e.g. "cdsc"), a from_item_ID, and its to_item_IDs, all
// encoded with idSize-byte item IDs (2 for iref version 0, 4 for version 1) —
// matching the layout rebuildIref parses: from_item_ID + reference_count +
// reference_count x to_item_ID.
func singleItemReferenceBox(refType string, idSize int, fromID uint32, toIDs []uint32) []byte {
	body := make([]byte, 0, idSize+2+len(toIDs)*idSize)
	if idSize == 2 {
		body = append(body, be16(uint16(fromID))...)
	} else {
		body = append(body, be32(fromID)...)
	}
	body = append(body, be16(uint16(len(toIDs)))...)
	for _, id := range toIDs {
		if idSize == 2 {
			body = append(body, be16(uint16(id))...)
		} else {
			body = append(body, be32(id)...)
		}
	}
	return mkBox(refType, body)
}

// irefBox builds a complete iref (ItemReferenceBox) FullBox of the given
// version (0 -> 16-bit item IDs in its children, 1 -> 32-bit), wrapping the
// given SingleItemTypeReferenceBox children (see singleItemReferenceBox).
func irefBox(version byte, children ...[]byte) []byte {
	var rest []byte
	for _, c := range children {
		rest = append(rest, c...)
	}
	return mkBox("iref", fullBoxBody(version, rest))
}

// buildAVIF assembles a minimal but spec-valid AVIF: ftyp (brand "avif"), a
// meta box containing hdlr, pitm (pointing at the first "av01" item, or
// items[0] if none), iinf (one infe per item), and iloc (one extent per item
// pointing into a trailing mdat), followed by mdat holding each item's bytes
// back-to-back in order. Reused by Task 2 for stripping tests.
//
// extraMetaChildren, if given, are appended to meta's children after iloc
// (e.g. an iref box built with irefBox) — placed after iloc so they cannot
// affect the iloc extent_offset patching below, which only depends on the
// lengths of hdlr/pitm/iinf/iloc's own header.
func buildAVIF(t *testing.T, items []testItem, extraMetaChildren ...[]byte) []byte {
	t.Helper()

	ftypBody := make([]byte, 0, 4+4+8)
	ftypBody = append(ftypBody, []byte("avif")...) // major_brand
	ftypBody = append(ftypBody, be32(0)...)        // minor_version
	ftypBody = append(ftypBody, []byte("avif")...) // compatible_brands...
	ftypBody = append(ftypBody, []byte("mif1")...)
	ftyp := mkBox("ftyp", ftypBody)

	var primaryID uint32
	if len(items) > 0 {
		primaryID = items[0].id
	}
	for _, it := range items {
		if it.itemType == "av01" {
			primaryID = it.id
			break
		}
	}

	hdlrRest := make([]byte, 0, 4+4+12+1)
	hdlrRest = append(hdlrRest, 0, 0, 0, 0)          // pre_defined
	hdlrRest = append(hdlrRest, []byte("pict")...)   // handler_type
	hdlrRest = append(hdlrRest, make([]byte, 12)...) // reserved
	hdlrRest = append(hdlrRest, 0x00)                // name (empty, null-terminated)
	hdlr := mkBox("hdlr", fullBoxBody(0, hdlrRest))

	pitm := mkBox("pitm", fullBoxBody(0, be16(uint16(primaryID))))

	iinf := iinfBox(items)

	// iloc: offset_size=4, length_size=4, base_offset_size=0, index_size=0;
	// one extent per item. extent_offset is written as a placeholder here and
	// patched below once the final file layout (and thus mdat offsets) is known.
	var ilocRest []byte
	ilocRest = append(ilocRest, 0x44, 0x00)
	ilocRest = append(ilocRest, be16(uint16(len(items)))...) // item_count
	patchPos := make([]int, len(items))                      // offsets within ilocRest
	for i, it := range items {
		ilocRest = append(ilocRest, be16(uint16(it.id))...) // item_ID
		ilocRest = append(ilocRest, be16(0)...)             // data_reference_index
		ilocRest = append(ilocRest, be16(1)...)             // extent_count
		patchPos[i] = len(ilocRest)
		ilocRest = append(ilocRest, be32(0)...)                    // extent_offset (placeholder)
		ilocRest = append(ilocRest, be32(uint32(len(it.data)))...) // extent_length
	}
	iloc := mkBox("iloc", fullBoxBody(0, ilocRest))

	var metaChildren []byte
	metaChildren = append(metaChildren, hdlr...)
	metaChildren = append(metaChildren, pitm...)
	metaChildren = append(metaChildren, iinf...)
	metaChildren = append(metaChildren, iloc...)
	for _, extra := range extraMetaChildren {
		metaChildren = append(metaChildren, extra...)
	}
	meta := mkBox("meta", fullBoxBody(0, metaChildren))

	prefix := make([]byte, 0, len(ftyp)+len(meta))
	prefix = append(prefix, ftyp...)
	prefix = append(prefix, meta...)

	// Absolute position (within prefix) of the iloc box's ilocRest bytes, so the
	// patchPos offsets (relative to ilocRest) can be translated to absolute
	// positions inside prefix.
	ilocRestBase := len(ftyp) + 8 /* meta header */ + 4 /* meta version/flags */ +
		len(hdlr) + len(pitm) + len(iinf) + 8 /* iloc header */ + 4 /* iloc version/flags */

	var mdatPayload []byte
	itemOffsetInMdat := make([]int, len(items))
	for i, it := range items {
		itemOffsetInMdat[i] = len(mdatPayload)
		mdatPayload = append(mdatPayload, it.data...)
	}

	const mdatHeaderLen = 8
	for i := range items {
		abs := len(prefix) + mdatHeaderLen + itemOffsetInMdat[i]
		pos := ilocRestBase + patchPos[i]
		if pos+4 > len(prefix) {
			t.Fatalf("iloc patch position out of range")
		}
		binary.BigEndian.PutUint32(prefix[pos:pos+4], uint32(abs))
	}

	out := make([]byte, 0, len(prefix)+mdatHeaderLen+len(mdatPayload))
	out = append(out, prefix...)
	out = append(out, mkBox("mdat", mdatPayload)...)
	return out
}

func TestWalkBoxes(t *testing.T) {
	t.Run("two boxes parse", func(t *testing.T) {
		data := append(mkBox("abcd", []byte("1234")), mkBox("efgh", []byte("hello!!!"))...)
		boxes, err := walkBoxes(data, 0, len(data))
		if err != nil {
			t.Fatalf("walkBoxes: %v", err)
		}
		if len(boxes) != 2 {
			t.Fatalf("got %d boxes, want 2", len(boxes))
		}
		if boxes[0].typ != "abcd" || boxes[0].start != 0 || boxes[0].headerLen != 8 || boxes[0].size != 12 {
			t.Errorf("box[0] = %+v", boxes[0])
		}
		if boxes[1].typ != "efgh" || boxes[1].start != 12 || boxes[1].headerLen != 8 || boxes[1].size != 16 {
			t.Errorf("box[1] = %+v", boxes[1])
		}
	})

	t.Run("truncated box errors", func(t *testing.T) {
		// Declares a size of 20 but only 10 bytes are actually present.
		data := append(be32(20), []byte("abcd12")...)
		if _, err := walkBoxes(data, 0, len(data)); err == nil {
			t.Errorf("expected error on truncated box")
		}
	})

	t.Run("truncated header errors", func(t *testing.T) {
		data := []byte{0x00, 0x00, 0x00} // fewer than 8 bytes, no full header
		if _, err := walkBoxes(data, 0, len(data)); err == nil {
			t.Errorf("expected error on truncated box header")
		}
	})

	t.Run("64-bit largesize parses", func(t *testing.T) {
		payload := []byte("payload-bytes")
		largesize := uint64(16 + len(payload))
		data := make([]byte, 0, largesize)
		data = append(data, be32(1)...) // size == 1 -> largesize follows
		data = append(data, []byte("xtyp")...)
		data = append(data, be64(largesize)...)
		data = append(data, payload...)

		boxes, err := walkBoxes(data, 0, len(data))
		if err != nil {
			t.Fatalf("walkBoxes: %v", err)
		}
		if len(boxes) != 1 {
			t.Fatalf("got %d boxes, want 1", len(boxes))
		}
		b := boxes[0]
		if b.typ != "xtyp" || b.headerLen != 16 || b.size != int(largesize) || b.start != 0 {
			t.Errorf("box = %+v, want typ=xtyp headerLen=16 size=%d start=0", b, largesize)
		}
	})

	t.Run("size zero extends to end", func(t *testing.T) {
		data := append(be32(0), []byte("ztyp")...)
		data = append(data, []byte("rest of the box")...)
		boxes, err := walkBoxes(data, 0, len(data))
		if err != nil {
			t.Fatalf("walkBoxes: %v", err)
		}
		if len(boxes) != 1 || boxes[0].size != len(data) {
			t.Fatalf("boxes = %+v, want single box spanning %d bytes", boxes, len(data))
		}
	})
}

func TestFindAVIFMetadataItems(t *testing.T) {
	t.Run("av01 + Exif + XMP returns exactly those ids", func(t *testing.T) {
		data := buildAVIF(t, []testItem{
			{id: 1, itemType: "av01", data: []byte("fake-av1-bitstream")},
			{id: 2, itemType: "Exif", data: []byte{0x4D, 0x4D, 0x00, 0x2A}},
			{id: 3, itemType: "mime", contentType: "application/rdf+xml", data: []byte("<x:xmpmeta/>")},
		})

		ids, err := findAVIFMetadataItems(data)
		if err != nil {
			t.Fatalf("findAVIFMetadataItems: %v", err)
		}
		want := map[uint32]bool{2: true, 3: true}
		if len(ids) != len(want) {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
		for id := range want {
			if !ids[id] {
				t.Errorf("missing expected id %d in %v", id, ids)
			}
		}
	})

	t.Run("av01 only returns empty set", func(t *testing.T) {
		data := buildAVIF(t, []testItem{
			{id: 1, itemType: "av01", data: []byte("fake-av1-bitstream")},
		})

		ids, err := findAVIFMetadataItems(data)
		if err != nil {
			t.Fatalf("findAVIFMetadataItems: %v", err)
		}
		if len(ids) != 0 {
			t.Errorf("ids = %v, want empty", ids)
		}
	})

	t.Run("truncated iinf errors", func(t *testing.T) {
		data := buildAVIF(t, []testItem{
			{id: 1, itemType: "av01", data: []byte("fake-av1-bitstream")},
			{id: 2, itemType: "Exif", data: []byte{0x4D, 0x4D, 0x00, 0x2A}},
			{id: 3, itemType: "mime", contentType: "application/rdf+xml", data: []byte("<x:xmpmeta/>")},
		})

		idx := bytes.Index(data, []byte("iinf"))
		if idx < 0 {
			t.Fatalf("fixture has no iinf box")
		}
		// Cut the buffer partway through the iinf box's infe children: the box
		// headers upstream (ftyp/meta/iinf) still declare their original,
		// now-too-large sizes, so this must surface as an error rather than a
		// panic or a silently wrong result.
		truncated := data[:idx+20]

		if _, err := findAVIFMetadataItems(truncated); err == nil {
			t.Errorf("expected error on truncated iinf")
		}
	})
}

// itemPayload re-parses data's iloc for the item with the given id (assumed
// single-extent, construction_method 0 into a top-level mdat) and returns its
// payload bytes sliced out of mdat. Used to assert losslessness of the av01
// item across a stripAVIF round-trip.
func itemPayload(t *testing.T, data []byte, id uint32) []byte {
	t.Helper()

	top, err := walkBoxes(data, 0, len(data))
	if err != nil {
		t.Fatalf("walkBoxes: %v", err)
	}
	metaBox, ok := findChild(top, "meta")
	if !ok {
		t.Fatalf("no meta box in output")
	}
	metaChildren, err := walkBoxes(data, metaBox.start+metaBox.headerLen+4, metaBox.start+metaBox.size)
	if err != nil {
		t.Fatalf("walking meta children: %v", err)
	}
	ilocB, ok := findChild(metaChildren, "iloc")
	if !ok {
		t.Fatalf("no iloc box in output")
	}
	parsed, err := parseIloc(data, ilocB)
	if err != nil {
		t.Fatalf("parseIloc: %v", err)
	}
	mdatB, ok := findChild(top, "mdat")
	if !ok {
		t.Fatalf("no mdat box in output")
	}
	mdatPayloadStart := mdatB.start + mdatB.headerLen

	for _, it := range parsed.items {
		if it.id != id {
			continue
		}
		if it.constructionMethod != 0 {
			t.Fatalf("item %d: unsupported construction_method %d in test helper", id, it.constructionMethod)
		}
		if len(it.extents) != 1 {
			t.Fatalf("item %d: expected exactly 1 extent, got %d", id, len(it.extents))
		}
		ext := it.extents[0]
		abs := it.baseOffset + ext.offset
		start := int(abs) - mdatPayloadStart
		end := start + int(ext.length)
		if start < 0 || end > len(data)-mdatPayloadStart {
			t.Fatalf("item %d: extent [%d,%d) out of bounds", id, start, end)
		}
		return data[mdatPayloadStart+start : mdatPayloadStart+end]
	}
	t.Fatalf("item %d not found in output iloc", id)
	return nil
}

func TestStripAVIF_RemovesExif(t *testing.T) {
	av01Data := []byte("fake-av1-bitstream-payload")
	data := buildAVIF(t, []testItem{
		{id: 1, itemType: "av01", data: av01Data},
		{id: 2, itemType: "Exif", data: []byte{0x4D, 0x4D, 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08}},
	})

	out, err := stripAVIF(data)
	if err != nil {
		t.Fatalf("stripAVIF: %v", err)
	}

	ids, err := findAVIFMetadataItems(out)
	if err != nil {
		t.Fatalf("findAVIFMetadataItems(out): %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("output still has metadata items: %v", ids)
	}

	if got := itemPayload(t, out, 1); !bytes.Equal(got, av01Data) {
		t.Errorf("av01 payload = %q, want %q", got, av01Data)
	}

	if len(out) >= len(data) {
		t.Errorf("output len %d not smaller than input len %d", len(out), len(data))
	}
}

func TestStripAVIF_RemovesXMP(t *testing.T) {
	av01Data := []byte("fake-av1-bitstream-payload")
	data := buildAVIF(t, []testItem{
		{id: 1, itemType: "av01", data: av01Data},
		{id: 2, itemType: "mime", contentType: "application/rdf+xml", data: []byte("<x:xmpmeta>hello</x:xmpmeta>")},
	})

	out, err := stripAVIF(data)
	if err != nil {
		t.Fatalf("stripAVIF: %v", err)
	}

	ids, err := findAVIFMetadataItems(out)
	if err != nil {
		t.Fatalf("findAVIFMetadataItems(out): %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("output still has metadata items: %v", ids)
	}

	if got := itemPayload(t, out, 1); !bytes.Equal(got, av01Data) {
		t.Errorf("av01 payload = %q, want %q", got, av01Data)
	}

	if len(out) >= len(data) {
		t.Errorf("output len %d not smaller than input len %d", len(out), len(data))
	}
}

func TestStripAVIF_RemovesBoth(t *testing.T) {
	av01Data := []byte("fake-av1-bitstream-payload")
	data := buildAVIF(t, []testItem{
		{id: 1, itemType: "av01", data: av01Data},
		{id: 2, itemType: "Exif", data: []byte{0x4D, 0x4D, 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08}},
		{id: 3, itemType: "mime", contentType: "application/rdf+xml", data: []byte("<x:xmpmeta>hello</x:xmpmeta>")},
	})

	out, err := stripAVIF(data)
	if err != nil {
		t.Fatalf("stripAVIF: %v", err)
	}

	ids, err := findAVIFMetadataItems(out)
	if err != nil {
		t.Fatalf("findAVIFMetadataItems(out): %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("output still has metadata items: %v", ids)
	}

	if got := itemPayload(t, out, 1); !bytes.Equal(got, av01Data) {
		t.Errorf("av01 payload = %q, want %q", got, av01Data)
	}

	if len(out) >= len(data) {
		t.Errorf("output len %d not smaller than input len %d", len(out), len(data))
	}
}

// TestStripAVIF_RemovesIrefToDeletedItem covers rebuildIref, which real
// libavif output exercises via a meta-level iref containing a "cdsc"
// (content-describes) reference from the Exif item to the primary av01 item.
func TestStripAVIF_RemovesIrefToDeletedItem(t *testing.T) {
	av01Data := []byte("fake-av1-bitstream-payload")
	exifData := []byte{0x4D, 0x4D, 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08}
	const av01ID, exifID = uint32(1), uint32(2)

	cdsc := singleItemReferenceBox("cdsc", 2, exifID, []uint32{av01ID})
	iref := irefBox(0, cdsc)

	data := buildAVIF(t, []testItem{
		{id: av01ID, itemType: "av01", data: av01Data},
		{id: exifID, itemType: "Exif", data: exifData},
	}, iref)

	out, err := stripAVIF(data)
	if err != nil {
		t.Fatalf("stripAVIF: %v", err)
	}

	// (1) output re-parses via walkBoxes/parseIloc without error.
	top, err := walkBoxes(out, 0, len(out))
	if err != nil {
		t.Fatalf("walkBoxes(out): %v", err)
	}
	metaBox, ok := findChild(top, "meta")
	if !ok {
		t.Fatalf("no meta box in output")
	}
	metaChildren, err := walkBoxes(out, metaBox.start+metaBox.headerLen+4, metaBox.start+metaBox.size)
	if err != nil {
		t.Fatalf("walking output meta children: %v", err)
	}
	ilocB, ok := findChild(metaChildren, "iloc")
	if !ok {
		t.Fatalf("no iloc box in output")
	}
	if _, err := parseIloc(out, ilocB); err != nil {
		t.Fatalf("parseIloc(out): %v", err)
	}

	// (2) findAVIFMetadataItems(output) is empty.
	ids, err := findAVIFMetadataItems(out)
	if err != nil {
		t.Fatalf("findAVIFMetadataItems(out): %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("output still has metadata items: %v", ids)
	}

	// (3) the primary av01 payload is byte-identical.
	if got := itemPayload(t, out, av01ID); !bytes.Equal(got, av01Data) {
		t.Errorf("av01 payload = %q, want %q", got, av01Data)
	}

	// (4) the reference to the removed Exif item is gone: since fromID ==
	// exifID (the removed item), rebuildIref drops the whole cdsc child, so
	// either iref is now absent or, if present, no child mentions exifID.
	irefOutB, hasIref := findChild(metaChildren, "iref")
	if hasIref {
		irefChildren, err := walkBoxes(out, irefOutB.start+irefOutB.headerLen+4, irefOutB.start+irefOutB.size)
		if err != nil {
			t.Fatalf("walking output iref children: %v", err)
		}
		for _, c := range irefChildren {
			bodyStart := c.start + c.headerLen
			fromID := uint32(binary.BigEndian.Uint16(out[bodyStart : bodyStart+2]))
			if fromID == exifID {
				t.Errorf("output iref still has a reference from removed item %d", exifID)
			}
			count := int(binary.BigEndian.Uint16(out[bodyStart+2 : bodyStart+4]))
			for i := 0; i < count; i++ {
				off := bodyStart + 4 + i*2
				toID := uint32(binary.BigEndian.Uint16(out[off : off+2]))
				if toID == exifID {
					t.Errorf("output iref still has a reference to removed item %d", exifID)
				}
			}
		}
	}
}

func TestStripAVIF_CleanIsByteIdentical(t *testing.T) {
	data := buildAVIF(t, []testItem{
		{id: 1, itemType: "av01", data: []byte("fake-av1-bitstream-payload")},
	})

	out, err := stripAVIF(data)
	if err != nil {
		t.Fatalf("stripAVIF: %v", err)
	}
	if !bytes.Equal(out, data) {
		t.Errorf("clean fixture: output not byte-identical to input")
	}
}

func TestStripAVIF_Idempotent(t *testing.T) {
	data := buildAVIF(t, []testItem{
		{id: 1, itemType: "av01", data: []byte("fake-av1-bitstream-payload")},
		{id: 2, itemType: "Exif", data: []byte{0x4D, 0x4D, 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08}},
	})

	out1, err := stripAVIF(data)
	if err != nil {
		t.Fatalf("stripAVIF (first pass): %v", err)
	}
	out2, err := stripAVIF(out1)
	if err != nil {
		t.Fatalf("stripAVIF (second pass): %v", err)
	}
	if !bytes.Equal(out1, out2) {
		t.Errorf("stripAVIF is not idempotent: first and second pass differ")
	}
}

func TestStripAVIF_Malformed(t *testing.T) {
	t.Run("truncated meta", func(t *testing.T) {
		data := buildAVIF(t, []testItem{
			{id: 1, itemType: "av01", data: []byte("fake-av1-bitstream-payload")},
			{id: 2, itemType: "Exif", data: []byte{0x4D, 0x4D, 0x00, 0x2A}},
		})
		idx := bytes.Index(data, []byte("iloc"))
		if idx < 0 {
			t.Fatalf("fixture has no iloc box")
		}
		// Cut partway through the iloc box's entries: upstream box headers
		// (ftyp/meta/iloc) still declare their original, now-too-large sizes.
		truncated := append([]byte(nil), data[:idx+12]...)

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("stripAVIF panicked: %v", r)
				}
			}()
			if _, err := stripAVIF(truncated); err == nil {
				t.Errorf("expected error on truncated meta/iloc")
			}
		}()
	})

	t.Run("iloc offset past EOF", func(t *testing.T) {
		data := buildAVIF(t, []testItem{
			{id: 1, itemType: "av01", data: []byte("fake-av1-bitstream-payload")},
			{id: 2, itemType: "Exif", data: []byte{0x4D, 0x4D, 0x00, 0x2A}},
		})
		mutated := append([]byte(nil), data...)
		// Find the iloc box and stomp the first extent_offset with a huge value.
		idx := bytes.Index(mutated, []byte("iloc"))
		if idx < 0 {
			t.Fatalf("fixture has no iloc box")
		}
		// iloc body layout: size(4)+type(4)+FullBox(4)+sizes(2)+item_count(2)+
		// [item_ID(2)+data_ref(2)+extent_count(2)+extent_offset(4)+extent_length(4)]...
		offsetFieldPos := idx + 4 + 4 + 2 + 2 + 2 + 2 + 2
		binary.BigEndian.PutUint32(mutated[offsetFieldPos:offsetFieldPos+4], 0xFFFFFFF0)

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("stripAVIF panicked: %v", r)
				}
			}()
			if _, err := stripAVIF(mutated); err == nil {
				t.Errorf("expected error on out-of-bounds iloc offset")
			}
		}()
	})

	t.Run("oversized box declares size past EOF", func(t *testing.T) {
		data := buildAVIF(t, []testItem{
			{id: 1, itemType: "av01", data: []byte("fake-av1-bitstream-payload")},
			{id: 2, itemType: "Exif", data: []byte{0x4D, 0x4D, 0x00, 0x2A}},
		})
		mutated := append([]byte(nil), data...)
		idx := bytes.Index(mutated, []byte("meta"))
		if idx < 0 {
			t.Fatalf("fixture has no meta box")
		}
		// meta's size field is the 4 bytes immediately before "meta".
		sizePos := idx - 4
		binary.BigEndian.PutUint32(mutated[sizePos:sizePos+4], 0x7FFFFFFF)

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("stripAVIF panicked: %v", r)
				}
			}()
			if _, err := stripAVIF(mutated); err == nil {
				t.Errorf("expected error on oversized meta box")
			}
		}()
	})
}

// replaceMetaChild rebuilds data with the meta box's child currently spanning
// [childStart, childStart+origSize) replaced by newChild, and meta's own size
// field adjusted to match. childStart/origSize must fall within the top-level
// meta box's byte range.
func replaceMetaChild(t *testing.T, data []byte, childStart, origSize int, newChild []byte) []byte {
	t.Helper()

	metaIdx := bytes.Index(data, []byte("meta"))
	if metaIdx < 0 {
		t.Fatalf("fixture has no meta box")
	}
	metaSizePos := metaIdx - 4
	origMetaSize := int(binary.BigEndian.Uint32(data[metaSizePos : metaSizePos+4]))
	newMetaSize := origMetaSize - origSize + len(newChild)

	out := make([]byte, 0, len(data)-origSize+len(newChild))
	out = append(out, data[:metaSizePos]...)
	out = append(out, be32(uint32(newMetaSize))...)
	out = append(out, data[metaSizePos+4:childStart]...)
	out = append(out, newChild...)
	out = append(out, data[childStart+origSize:]...)
	return out
}

// TestStripAVIF_HostileItemCount covers the DoS fix in parseIloc: a hostile
// iloc box (version 2, so item_count is 32-bit) that declares item_count =
// 0xFFFFFFFF while carrying none of the ~171GB of bytes such a count would
// require. Before the item_count guard, make([]ilocItem, 0, itemCount) ran
// before any per-item bounds check, so this input would trigger a fatal
// (unrecoverable) runtime out-of-memory on strict-overcommit hosts rather
// than a clean error — stripAVIF's deferred recover() cannot catch a fatal
// OOM. This must return an error, not panic and not OOM.
func TestStripAVIF_HostileItemCount(t *testing.T) {
	data := buildAVIF(t, []testItem{
		{id: 1, itemType: "av01", data: []byte("fake-av1-bitstream-payload")},
		{id: 2, itemType: "Exif", data: []byte{0x4D, 0x4D, 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08}},
	})

	ilocIdx := bytes.Index(data, []byte("iloc"))
	if ilocIdx < 0 {
		t.Fatalf("fixture has no iloc box")
	}
	ilocStart := ilocIdx - 4
	origIlocSize := int(binary.BigEndian.Uint32(data[ilocStart : ilocStart+4]))

	// version 2 (32-bit item_count), offset_size=4, length_size=4,
	// base_offset_size=0, index_size=0, item_count=0xFFFFFFFF: about 18 bytes
	// on the wire, declaring four billion items.
	hostileRest := append([]byte{0x44, 0x00}, be32(0xFFFFFFFF)...)
	hostileIloc := mkBox("iloc", fullBoxBody(2, hostileRest))

	mutated := replaceMetaChild(t, data, ilocStart, origIlocSize, hostileIloc)

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("stripAVIF panicked on hostile iloc item_count: %v", r)
			}
		}()
		out, err := stripAVIF(mutated)
		if err == nil {
			t.Fatalf("expected error on hostile iloc item_count, got success (out len %d)", len(out))
		}
		// Assert the item_count guard specifically fired (rather than some
		// unrelated later truncation error), so this test actually exercises
		// the pre-allocation bounds check and not just "some error happened".
		if !strings.Contains(err.Error(), "item_count") || !strings.Contains(err.Error(), "exceeds box size") {
			t.Errorf("expected an item_count-exceeds-box-size error, got: %v", err)
		}
	}()
}

// TestStripAVIF_HostileExtentCount is the same defense-in-depth guard applied
// to the per-item extent_count allocation: a single iloc item declaring
// extent_count = 0xFFFF (its max, extent_count being 16-bit) while the box
// carries none of the extent bytes such a count would require. extent_count
// being only 16-bit makes this a much smaller allocation than the item_count
// case, but the guard still fails closed before allocating rather than
// relying on the per-field bounds checks inside the extent loop.
func TestStripAVIF_HostileExtentCount(t *testing.T) {
	data := buildAVIF(t, []testItem{
		{id: 1, itemType: "av01", data: []byte("fake-av1-bitstream-payload")},
		{id: 2, itemType: "Exif", data: []byte{0x4D, 0x4D, 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08}},
	})

	ilocIdx := bytes.Index(data, []byte("iloc"))
	if ilocIdx < 0 {
		t.Fatalf("fixture has no iloc box")
	}
	ilocStart := ilocIdx - 4
	origIlocSize := int(binary.BigEndian.Uint32(data[ilocStart : ilocStart+4]))

	// version 0 (16-bit item_count/item_ID), offset_size=4, length_size=4,
	// base_offset_size=0, index_size=0, one item declaring extent_count =
	// 0xFFFF while carrying zero extents.
	hostileRest := []byte{0x44, 0x00}                  // sizes byte0/byte1
	hostileRest = append(hostileRest, be16(1)...)      // item_count = 1
	hostileRest = append(hostileRest, be16(1)...)      // item_ID
	hostileRest = append(hostileRest, be16(0)...)      // data_reference_index
	hostileRest = append(hostileRest, be16(0xFFFF)...) // extent_count = 0xFFFF
	hostileIloc := mkBox("iloc", fullBoxBody(0, hostileRest))

	mutated := replaceMetaChild(t, data, ilocStart, origIlocSize, hostileIloc)

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("stripAVIF panicked on hostile iloc extent_count: %v", r)
			}
		}()
		out, err := stripAVIF(mutated)
		if err == nil {
			t.Fatalf("expected error on hostile iloc extent_count, got success (out len %d)", len(out))
		}
		// Assert the extent_count guard specifically fired, not some other
		// truncation error further down the parse.
		if !strings.Contains(err.Error(), "extent_count") || !strings.Contains(err.Error(), "exceeds box size") {
			t.Errorf("expected an extent_count-exceeds-box-size error, got: %v", err)
		}
	}()
}
