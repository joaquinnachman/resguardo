import struct, sys
def check(data, base=0):
    magic, cputype, cpusub, ftype, ncmds, sizecmds, flags, _ = struct.unpack("<8I", data[base:base+32])
    off = base + 32
    sig = False
    for _ in range(ncmds):
        cmd, size = struct.unpack("<II", data[off:off+8])
        if cmd == 0x1d: sig = True
        off += size
    return hex(cputype), sig
data = open(sys.argv[1], "rb").read()
if struct.unpack(">I", data[:4])[0] == 0xCAFEBABE:
    n = struct.unpack(">I", data[4:8])[0]
    for i in range(n):
        ct, cs, off, size, al = struct.unpack(">5I", data[8+20*i:28+20*i])
        print("slice", check(data, off))
else:
    print(check(data))
