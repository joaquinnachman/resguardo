"""Une binarios Mach-O de distintas arquitecturas en uno universal (como `lipo -create`)."""
import struct, sys

def main(out, inputs):
    slices = []
    for p in inputs:
        data = open(p, "rb").read()
        magic, cputype, cpusub = struct.unpack("<III", data[:12])
        assert magic == 0xFEEDFACF, p + ": no es Mach-O de 64 bits"
        align = 14 if cputype == 0x0100000C else 12
        slices.append((cputype, cpusub, data, align))
    header = struct.pack(">II", 0xCAFEBABE, len(slices))
    offset = 8 + 20 * len(slices)
    entries, blobs = b"", []
    for cputype, cpusub, data, align in slices:
        a = 1 << align
        offset = (offset + a - 1) // a * a
        entries += struct.pack(">IIIII", cputype, cpusub, offset, len(data), align)
        blobs.append((offset, data))
        offset += len(data)
    out_data = bytearray(header + entries)
    for off, data in blobs:
        out_data += b"\0" * (off - len(out_data))
        out_data += data
    open(out, "wb").write(out_data)

if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2:])
