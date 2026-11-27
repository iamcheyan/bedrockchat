package update

import (
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Read only ELF's fixed-size executable/program headers. Section tables (which
// are not needed by the loader) can contain untrusted offsets and sizes.
func checkELF(r io.ReaderAt, size uint64, arch string) error {
	var machine elf.Machine
	switch arch {
	case "amd64":
		machine = elf.EM_X86_64
	case "arm64":
		machine = elf.EM_AARCH64
	default:
		return fmt.Errorf("unsupported ELF architecture %s", arch)
	}
	var header [64]byte
	if _, err := r.ReadAt(header[:], 0); err != nil {
		return fmt.Errorf("read ELF header: %w", err)
	}
	bo := binary.LittleEndian
	osabi := elf.OSABI(header[7])
	kind := elf.Type(bo.Uint16(header[16:]))
	if string(header[:7]) != "\x7fELF\x02\x01\x01" || (osabi != elf.ELFOSABI_NONE && osabi != elf.ELFOSABI_LINUX) ||
		(kind != elf.ET_EXEC && kind != elf.ET_DYN) || elf.Machine(bo.Uint16(header[18:])) != machine ||
		bo.Uint32(header[20:]) != 1 || bo.Uint16(header[52:]) != 64 {
		return fmt.Errorf("binary is not a compatible Linux %s ELF executable", arch)
	}
	entry := bo.Uint64(header[24:])
	off := bo.Uint64(header[32:])
	count := uint64(bo.Uint16(header[56:]))
	var ph [56]byte
	stride := uint64(len(ph))
	if entry == 0 || off < uint64(len(header)) || off > size || count == 0 || count > 1024 ||
		bo.Uint16(header[54:]) != uint16(stride) || count*stride > size-off {
		return errors.New("invalid ELF program header table")
	}
	executableEntry := false
	for i := uint64(0); i < count; i++ {
		if _, err := r.ReadAt(ph[:], int64(off+i*stride)); err != nil {
			return fmt.Errorf("read ELF program header: %w", err)
		}
		switch elf.ProgType(bo.Uint32(ph[:])) {
		case elf.PT_INTERP:
			return errors.New("release binary requires an external ELF interpreter")
		case elf.PT_LOAD:
			fileOffset, address := bo.Uint64(ph[8:]), bo.Uint64(ph[16:])
			fileSize, memSize, align := bo.Uint64(ph[32:]), bo.Uint64(ph[40:]), bo.Uint64(ph[48:])
			if fileOffset > size || fileSize > size-fileOffset || fileSize > memSize || address+memSize < address ||
				(align > 1 && (align&(align-1) != 0 || address%align != fileOffset%align)) {
				return errors.New("invalid ELF load segment")
			}
			if elf.ProgFlag(bo.Uint32(ph[4:]))&elf.PF_X != 0 && entry >= address && entry-address < fileSize {
				executableEntry = true
			}
		}
	}
	if !executableEntry {
		return errors.New("ELF entry point is not in a file-backed executable segment")
	}
	return nil
}
