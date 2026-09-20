#!/usr/bin/env python3
"""SPD dump validator: length / DRAM type code / CRC16-CCITT / XMP / EXPO magic."""
import sys, os, json

def crc16(data):
    crc = 0
    for b in data:
        crc ^= (b << 8) & 0xFFFF
        for _ in range(8):
            crc = ((crc << 1) ^ 0x1021) & 0xFFFF if (crc & 0x8000) else (crc << 1) & 0xFFFF
    return crc

TYPE_NAMES = {
    0x08: 'DDR2', 0x0B: 'DDR3',
    0x0C: 'DDR4', 0x0E: 'DDR4', 0x0F: 'DDR4', 0x10: 'DDR4', 0x11: 'DDR4',
    0x12: 'DDR5', 0x13: 'DDR5', 0x14: 'DDR5', 0x15: 'DDR5',
}
EXPECT_LEN = {0x08: 256, 0x0B: 256, 0x0C: 512, 0x0E: 512, 0x0F: 512, 0x10: 512, 0x11: 512,
              0x12: 1024, 0x13: 1024, 0x14: 1024, 0x15: 1024}

def analyze(path):
    d = open(path, 'rb').read()
    r = {'file': os.path.basename(path), 'size': len(d)}
    if len(d) not in (256, 512, 1024):
        r['error'] = 'bad length'
        return r
    if all(b == 0 for b in d):
        r['error'] = 'all zero'
        return r
    if all(b == 0xFF for b in d):
        r['error'] = 'all FF'
        return r
    tc = d[2]
    r['type_code'] = '0x%02X' % tc
    r['type'] = TYPE_NAMES.get(tc, 'UNKNOWN')
    r['len_matches_type'] = (EXPECT_LEN.get(tc) == len(d))
    # raw[0:128] = bytes 0..127 as raw card, byte0=#bytes used, byte1=SPD revision
    r['bytes_used'] = d[0]
    r['spd_rev'] = '%d.%d' % (d[1] >> 4, d[1] & 0xF)
    r['mfg_id'] = '0x%02X%02X' % (d[321], d[320]) if len(d) >= 512 else 'n/a'
    r['dram_mfg'] = '0x%02X%02X' % (d[351], d[350]) if len(d) >= 512 else 'n/a'
    if tc == 0x12 or (len(d) == 1024 and 0x12 <= tc <= 0x15):
        r['dram_mfg'] = '0x%02X%02X' % (d[552], d[551])
    # CRC
    def crc_ok(seg, lo, hi):
        return crc16(d[seg[0]:seg[1]]) == (d[lo] | (d[hi] << 8))
    if len(d) == 256:
        # JEDEC DDR3 (JESD21-C Annex K): CRC is always stored at bytes 126..127.
        # The coverage length is declared by byte 0 bit 7: bit7=1 -> bytes 0..116,
        # bit7=0 -> bytes 0..125.  The flat "bytes[0:126]" rule only holds for
        # modules whose vendor declared the full 126-byte coverage.
        if d[2] == 0x0B:
            end = 117 if (d[0] & 0x80) else 126
            r['crc_ok'] = crc_ok((0, end), 126, 127)
            r['crc_detail'] = 'bytes[0:%d] -> bytes[126:128] (byte0 bit7=%d)' % (end, (d[0] >> 7) & 1)
            r['crc_ok_task_rule'] = crc_ok((0, 126), 126, 127)
        else:
            r['crc_ok'] = crc_ok((0, 126), 126, 127)
            r['crc_detail'] = 'bytes[0:126] -> bytes[126:128]'
    elif len(d) == 512:
        c1 = crc_ok((0, 126), 126, 127)
        c2 = crc_ok((128, 254), 254, 255)
        r['crc_ok'] = c1 and c2
        r['crc_block1'] = c1
        r['crc_block2'] = c2
    else:
        r['crc_ok'] = crc_ok((0, 510), 510, 511)
        r['crc_detail'] = 'bytes[0:510] -> bytes[510:512]'
    # magic
    # XMP 1.x (DDR3) header "0C 4A" sits at bytes 176-177 in the 256-byte image.
    r['xmp1'] = (len(d) == 256 and d[176] == 0x0C and d[177] == 0x4A)
    r['xmp2'] = (len(d) >= 386 and d[384] == 0x0C and d[385] == 0x4A)
    r['xmp3'] = (len(d) >= 642 and d[640] == 0x0C and d[641] == 0x4A)
    r['expo'] = (len(d) >= 836 and bytes(d[832:836]) == b'EXPO')
    if len(d) >= 512:
        r['b384_385'] = '%02X %02X' % (d[384], d[385])
    if len(d) >= 1024:
        r['b640_641'] = '%02X %02X' % (d[640], d[641])
        r['b832_835'] = d[832:836].hex(' ').upper() + ' (' + ''.join(chr(c) if 32 <= c < 127 else '.' for c in d[832:836]) + ')'
    return r

if __name__ == '__main__':
    paths = sys.argv[1:]
    if paths and os.path.isdir(paths[0]) and len(paths) == 1:
        paths = sorted(os.path.join(paths[0], f) for f in os.listdir(paths[0])
                       if os.path.isfile(os.path.join(paths[0], f)))
    out = [analyze(p) for p in paths]
    print(json.dumps(out, indent=1, ensure_ascii=False))
