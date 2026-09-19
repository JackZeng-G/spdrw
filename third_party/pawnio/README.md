# PawnIO 模块与库（第三方二进制）

- `SmbusI801.bin` / `SmbusPIIX4.bin` / `SmbusIntelSkylakeIMC.bin`
  来自 [namazso/PawnIO.Modules](https://github.com/namazso/PawnIO.Modules) release 0.2.11，
  官方签名，未做任何修改。许可见 `COPYING.MODULES`（LGPL-2.1-or-later）。
- `PawnIOLib.dll` 来自 [OpenRGB dependencies/PawnIO](https://github.com/CalcProgrammer1/OpenRGB/tree/master/dependencies/PawnIO)
  （PawnIO 官方用户态库，LGPL-2.1）。源码: https://github.com/namazso/PawnIO (PawnIOLib)。
- 模块功能：内核侧沙箱化的 SMBus 主控驱动（Intel PCH i801 / AMD PIIX4(KernCZ) / Intel Skylake-X IMC）。
