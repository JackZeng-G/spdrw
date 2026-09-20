// Wails 绑定桩(冒烟/真页面注入共用)。新增绑定方法后要同步补这里, 否则调用会报“找不到方法”。
// ---- Wails 绑定桩(必须在 app.js 之前定义) ----
  window.__CALLS = [];
  window.__ERRORS = [];
  window.addEventListener("error", (e) => window.__ERRORS.push("error: " + e.message));
  window.addEventListener("unhandledrejection", (e) => window.__ERRORS.push("reject: " + (e.reason && e.reason.message || e.reason)));
  const rec = (name, ret) => (...args) => {
    window.__CALLS.push({ name, args });
    return Promise.resolve(typeof ret === "function" ? ret(...args) : ret);
  };
  const dumpB64 = (() => {
    const d = new Uint8Array(512);
    d[2] = 0x0c;
    let s = "";
    for (const b of d) s += String.fromCharCode(b);
    return btoa(s);
  })();
  const ddr4Status = {
    ddr5: false, generation: "DDR4", blocks: 4, blockSize: 128,
    protected: [true, false, false, false], known: [true, true, true, true],
    mr11: 0, mr12: 0, mr13: 0, mr29: 0, mr48: 0, mr52: 0,
    protectionHit: false, offline: false, pswpApplicable: true, pswp: false,
    warnings: ["DDR4 无写保护状态寄存器: 状态由块首写测试得出"],
  };
  const editState = { source: "设备 0x50", generation: "DDR4", size: 512, dirty: true, changeCount: 3, crcOk: true, canWrite: true, crcStale: false };
  const editFields = [
    { key: "partNumber", name: "部件号", group: "常用信息", kind: "string", value: "TEST-PN", offset: "0x149-20B", risk: "low" },
    { key: "serial", name: "序列号(hex)", group: "常用信息", kind: "hex", value: "DEADBEEF", offset: "0x145-4B", risk: "low" },
    { key: "ddr4.tAA", name: "tAA", group: "JEDEC 时序", kind: "float", unit: "ns", value: "1.5", offset: "0x18/0x7B", risk: "medium" },
  ];
  const editDiff = {
    changes: [{ offset: 325, old: 0xde, new: 0x11, field: "序列号", risk: "low" }],
    fields: [{ region: "序列号", risk: "low", count: 4, ranges: "0x145-0x148" }],
    highRisk: 0, crcFields: 2, changeCount: 3, crcOk: true, truncated: false,
    crcDirty: 0, crcFreeDirty: 3, dirtyInCrc: [], dirtyFree: [323, 325, 329],
  };
  // 校验状态: 桩里的 dump 是 512B DDR4(byte2=0x0C), 两段各 128B
  const crcStatus = {
    generation: "DDR4", size: 512, known: true, ok: true, covered: 252,
    crcBytes: [126, 127, 254, 255],
    ranges: [
      { name: "块 1(0x000-0x07D)", start: 0, end: 126, crcOff: 126, crcLen: 2 },
      { name: "块 2(0x080-0x0FD)", start: 128, end: 254, crcOff: 254, crcLen: 2 },
    ],
    freeAreas: [
      { name: "厂商", start: 320, end: 322 },
      { name: "生产日期", start: 323, end: 325 },
      { name: "序列号", start: 325, end: 329 },
      { name: "部件号", start: 329, end: 349 },
    ],
  };
  const preflight = {
    path: "/tmp/dump.bin", addr: 0x50, generation: "DDR4", deviceSize: 512, fileSize: 512, sizeOk: true,
    changeCount: 3, crcBytes: 2, changes: [
      { offset: 325, old: 1, new: 0xab, block: 2, isCRC: false },
      { offset: 126, old: 0x11, new: 0x40, block: 0, isCRC: true },
    ],
    changesTruncated: false, fields: [{ region: "序列号", risk: "low", count: 1, ranges: "0x145" }],
    highRiskCount: 0, protectedBlocks: [], unknownBlocks: [], pswp: false,
    targetCrcValid: true, currentCrcValid: true, dryRun: false, warnings: [], blocked: false, blockReason: "", blockKind: "",
  };
  window.go = { app: { App: {
    ListControllers: rec("ListControllers", [{ index: 0, name: "Intel PCH SMBus (I801)", kind: "i801", noSpdWp: true, wpKnown: true }]),
    AutoConnectAll: rec("AutoConnectAll", { ctlIndex: 0, dimms: [{ addr: 0x50, isDdr5: false, ramType: "DDR4", size: 512 }] }),
    Connect: rec("Connect", null),
    Scan: rec("Scan", [{ addr: 0x50, isDdr5: false, ramType: "DDR4", size: 512 }]),
    Select: rec("Select", null),
    Dump: rec("Dump", dumpB64),
    Decode: rec("Decode", { valid: true, ramType: "DDR4", moduleType: "UDIMM", totalMib: 8192, totalHuman: "8 GiB", ranks: 2, deviceWidth: 8, busWidth: 64, manufacturer: "Micron Technology", partNumber: "TEST-PN", dateYear: 2024, dateWeek: 15, serialHex: "DEADBEEF", crcOk: true, hasTimings: true, tck: { ns: 0.75, cycles: 0 }, casLatencies: "17,18,19,20" }),
    ReadFileBytes: rec("ReadFileBytes", dumpB64),
    SaveDumpDialog: rec("SaveDumpDialog", null),
    DecodeFileDialog: rec("DecodeFileDialog", null),
    VerifyFileDialog: rec("VerifyFileDialog", "/tmp/x.bin"),
    PickWriteFile: rec("PickWriteFile", "/tmp/dump.bin"),
    PreflightWrite: rec("PreflightWrite", preflight),
    WriteConfirmed: rec("WriteConfirmed", { dryRun: false, written: 3, total: 3, backupPath: "/root/.spdrw/backups/x.bin", verified: true, message: "写入并校验通过" }),
    SetFastRead: rec("SetFastRead", true),
    ReadStats: rec("ReadStats", { bytes: 1024, transactions: 33, blockBytes: 1024, fallbackBytes: 0, blockReadOK: true, blockReadKnown: true }),
    BusStats: rec("BusStats", { generation: "DDR4", reads: 2140, quickWrites: 3, byteDataWrites: 2, byteWrites: 14, nvmWrites: 0 }),
    BusTuning: rec("BusTuning", { tunable: true, clockHz: 396000, sleepMode: 0, sleepModeName: "忙等(最快)", fastRead: true }),
    SetSleepMode: rec("SetSleepMode", 0),
    WriteProbe: rec("WriteProbe", { addr: 80, offset: 560, offsetText: "0x230", old: 0, new: 255, readBack: 255, verdict: "ok", note: "写入生效", restored: true, verified: true, backupPath: "/tmp/b.bin" }),
    ResetBusStats: rec("ResetBusStats", null),
    WPStatus: rec("WPStatus", ddr4Status),
    WPSet: rec("WPSet", null),
    WPClear: rec("WPClear", null),
    EditLoadFromDevice: rec("EditLoadFromDevice", editState),
    EditLoadFileDialog: rec("EditLoadFileDialog", editState),
    EditFields: rec("EditFields", editFields),
    EditSetField: rec("EditSetField", editState),
    EditSetByte: rec("EditSetByte", editState),
    EditFixCRC: rec("EditFixCRC", editState),
    EditReset: rec("EditReset", { ...editState, dirty: false, changeCount: 0 }),
    EditDiff: rec("EditDiff", editDiff),
    EditBytes: rec("EditBytes", dumpB64),
    EditVerifyFile: rec("EditVerifyFile", { changes: [], fields: [], highRisk: 0, crcFields: 0, changeCount: 0, crcOk: true, truncated: false }),
    EditExportDialog: rec("EditExportDialog", "/tmp/edited.bin"),
    EditApplyToDevice: rec("EditApplyToDevice", { dryRun: false, written: 3, total: 3, backupPath: "/root/.spdrw/backups/e.bin", verified: true, message: "写入并校验通过" }),
    EditState: rec("EditState", editState),
    CRCStatus: rec("CRCStatus", crcStatus),
    MfgSearch: rec("MfgSearch", [{ name: "Micron Technology", cont: 0x80, code: 0x2c }]),
  } } };
  window.runtime = { EventsOn: () => {} };
