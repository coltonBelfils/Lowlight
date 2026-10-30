#!/usr/bin/env python3
"""
Builds a self-contained, zero-dependency VS Code `.vsix` package for Lowlight.

Steps performed:
1. Compiles `./lowlight` (`go build -ldflags="-s -w" -o lowlight ./cmd/lowlight`).
2. Copies the compiled binary into `editors/vscode/lowlight/bin/lowlight` (mode 0755)
   and copies `std/*.ll` into `editors/vscode/lowlight/std/` so the extension works
   out-of-the-box on `.ll` files anywhere on disk.
3. Packages `editors/vscode/lowlight-vscode-<version>.vsix` conforming to the VS Code
   Open Packaging Conventions (OPC) VSIX specification (`[Content_Types].xml` +
   `extension.vsixmanifest` + `extension/...` payload with preserved Unix file modes).
"""

import json
import os
import shutil
import stat
import subprocess
import sys
import zipfile
from pathlib import Path
from xml.sax.saxutils import escape


CONTENT_TYPES_XML = """<?xml version="1.0" encoding="utf-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="json" ContentType="application/json"/>
  <Default Extension="js" ContentType="application/javascript"/>
  <Default Extension="md" ContentType="text/markdown"/>
  <Default Extension="ll" ContentType="text/plain"/>
  <Default Extension="txt" ContentType="text/plain"/>
  <Default Extension="exe" ContentType="application/octet-stream"/>
  <Default Extension="vsixmanifest" ContentType="text/xml"/>
  <Override PartName="/extension/bin/lowlight" ContentType="application/octet-stream"/>
</Types>
"""


def make_vsix_manifest(pkg: dict) -> str:
    publisher = escape(pkg["publisher"])
    name = escape(pkg["name"])
    version = escape(pkg["version"])
    display_name = escape(pkg.get("displayName", name))
    description = escape(pkg.get("description", ""))
    engine = escape(pkg.get("engines", {}).get("vscode", "^1.75.0"))
    categories = escape(",".join(pkg.get("categories", ["Programming Languages"])))

    return f"""<?xml version="1.0" encoding="utf-8"?>
<PackageManifest Version="2.0.0" xmlns="http://schemas.microsoft.com/developer/vsx-schema/2011" xmlns:d="http://schemas.microsoft.com/developer/vsx-schema-design/2011">
  <Metadata>
    <Identity Language="en-US" Id="{name}" Version="{version}" Publisher="{publisher}"/>
    <DisplayName>{display_name}</DisplayName>
    <Description xml:space="preserve">{description}</Description>
    <Tags>lowlight,lsp,systems,programming-language</Tags>
    <Categories>{categories}</Categories>
    <GalleryFlags>Public</GalleryFlags>
  </Metadata>
  <Installation>
    <InstallationTarget Id="Microsoft.VisualStudio.Code"/>
  </Installation>
  <Dependencies/>
  <Assets>
    <Asset Type="Microsoft.VisualStudio.Code.Manifest" Path="extension/package.json" Addressable="true"/>
    <Asset Type="Microsoft.VisualStudio.Services.Content.Details" Path="extension/README.md" Addressable="true"/>
  </Assets>
</PackageManifest>
"""


def add_bytes_to_zip(zf: zipfile.ZipFile, arcname: str, data: bytes, mode: int = 0o644) -> None:
    info = zipfile.ZipInfo(arcname)
    info.date_time = (2026, 1, 1, 0, 0, 0)
    info.compress_type = zipfile.ZIP_DEFLATED
    info.create_system = 3  # Unix
    info.external_attr = (stat.S_IFREG | mode) << 16
    zf.writestr(info, data)


def main() -> int:
    script_dir = Path(__file__).resolve().parent
    repo_root = script_dir.parent.parent
    ext_dir = script_dir / "lowlight"
    pkg_json_path = ext_dir / "package.json"

    if not pkg_json_path.is_file():
        print(f"error: missing {pkg_json_path}", file=sys.stderr)
        return 1

    with pkg_json_path.open("r", encoding="utf-8") as f:
        pkg = json.load(f)

    exe_name = "lowlight.exe" if os.name == "nt" else "lowlight"
    root_bin = repo_root / exe_name

    print(f"[1/3] Building {root_bin.name} (with `lowlight lsp`)...")
    subprocess.run(
        ["go", "build", "-ldflags=-s -w", "-o", str(root_bin), "./cmd/lowlight"],
        cwd=str(repo_root),
        check=True,
    )

    print("[2/3] Bundling compiler/LSP binary and `std/` library into extension...")
    bundled_bin_dir = ext_dir / "bin"
    bundled_bin_dir.mkdir(parents=True, exist_ok=True)
    bundled_bin = bundled_bin_dir / exe_name
    shutil.copy2(root_bin, bundled_bin)
    if os.name != "nt":
        os.chmod(bundled_bin, 0o755)

    bundled_std_dir = ext_dir / "std"
    if bundled_std_dir.exists():
        shutil.rmtree(bundled_std_dir)
    bundled_std_dir.mkdir(parents=True, exist_ok=True)
    for ll_file in sorted((repo_root / "std").glob("*.ll")):
        shutil.copy2(ll_file, bundled_std_dir / ll_file.name)

    version = pkg["version"]
    vsix_path = script_dir / f"lowlight-vscode-{version}.vsix"
    print(f"[3/3] Creating {vsix_path.relative_to(repo_root)}...")

    with zipfile.ZipFile(vsix_path, "w", compression=zipfile.ZIP_DEFLATED) as zf:
        add_bytes_to_zip(zf, "[Content_Types].xml", CONTENT_TYPES_XML.encode("utf-8"), 0o644)
        add_bytes_to_zip(zf, "extension.vsixmanifest", make_vsix_manifest(pkg).encode("utf-8"), 0o644)

        for file_path in sorted(ext_dir.rglob("*")):
            if file_path.is_dir() or file_path.name == ".DS_Store":
                continue
            rel = file_path.relative_to(ext_dir).as_posix()
            arcname = f"extension/{rel}"
            is_exec = (rel == f"bin/{exe_name}") or os.access(file_path, os.X_OK)
            mode = 0o755 if is_exec else 0o644
            add_bytes_to_zip(zf, arcname, file_path.read_bytes(), mode)

    size_kb = vsix_path.stat().st_size / 1024.0
    print(f"Done! Created {vsix_path} ({size_kb:.1f} KB)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
