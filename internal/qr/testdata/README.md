# internal/qr 的对照数据

`golden.json` 与 `golden_auto.json` 是**外部实现生成的参照矩阵**，用来证明本包的编码结果
与规范一致；`go test ./internal/qr/` 会逐模块比对。

| 文件 | 生成者 | 用途 |
|---|---|---|
| `golden.json` | Python [`qrcode`](https://pypi.org/project/qrcode/)（强制掩码 0–7） | 校验数据段、Reed–Solomon 分块与交织、掩码、格式/版本信息 |
| `golden_auto.json` | Nayuki [`qrcodegen`](https://pypi.org/project/qrcodegen/)（自动掩码） | 校验罚分选优（规范级实现，因为 `qrcode` 的罚分与规范有出入） |

两个文件的结构一致，每条是 `{text, version, mask, rows}`，`rows` 是每一行自左向右的
`0/1` 字符串（`1` = 深色模块，已包含掩码与格式信息，没有静区）。

## 重新生成

```bash
pip install qrcode qrcodegen

python - <<'PY'
import json, qrcode
from qrcode.constants import ERROR_CORRECT_M

texts = ["https://lanbox.local", "https://192.168.1.9:8443", "https://lanbox.local/" + "a"*120]
cases = []
for text in texts:
    base = qrcode.QRCode(error_correction=ERROR_CORRECT_M, box_size=1, border=0)
    base.add_data(text); base.make(fit=True)
    for mask in range(8):
        qr = qrcode.QRCode(version=base.version, error_correction=ERROR_CORRECT_M, box_size=1, border=0)
        qr.add_data(text); qr.makeImpl(False, mask)
        cases.append({"text": text, "version": base.version, "mask": mask, "auto": False,
                      "rows": ["".join("1" if c else "0" for c in r) for r in qr.modules]})
json.dump(cases, open("internal/qr/testdata/golden.json", "w"), indent=0)
PY

python - <<'PY'
import json
from qrcodegen import QrCode, QrSegment

texts = ["https://lanbox.local", "https://lanbox.local:8443", "https://192.168.1.9:8443",
         "https://lanbox.local/" + "a"*120, "https://192.168.1.9:8443/#lanbox"]
cases = []
for text in texts:
    qr = QrCode.encode_segments([QrSegment.make_bytes(text.encode())], QrCode.Ecc.MEDIUM, 1, 10, -1, False)
    n = qr.get_size()
    cases.append({"text": text, "version": qr.get_version(), "mask": qr.get_mask(),
                  "rows": ["".join("1" if qr.get_module(x, y) else "0" for x in range(n)) for y in range(n)]})
json.dump(cases, open("internal/qr/testdata/golden_auto.json", "w"), indent=0)
PY
```
