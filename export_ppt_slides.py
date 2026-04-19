from __future__ import annotations

import argparse
import math
import shutil
import sys
import tempfile
from pathlib import Path


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Export each slide/page in a PowerPoint or PDF file as a PNG image."
    )
    parser.add_argument(
        "input_path",
        nargs="?",
        default="ppt.pptx",
        help="Path to the .pptx or .pdf file. Defaults to ppt.pptx in the current directory.",
    )
    parser.add_argument(
        "-o",
        "--output-dir",
        default=None,
        help="Directory to save exported slide images. Defaults to <PPT name>_截图.",
    )
    parser.add_argument(
        "--width",
        type=int,
        default=1920,
        help="Export width in pixels. Default: 1920.",
    )
    parser.add_argument(
        "--height",
        type=int,
        default=1080,
        help="Export height in pixels. Default: 1080.",
    )
    return parser.parse_args()


def build_output_dir(pptx_path: Path, output_dir: str | None) -> Path:
    if output_dir:
        return Path(output_dir).expanduser().resolve()
    return pptx_path.with_name(f"{pptx_path.stem}_截图")


def export_pdf_pages(pdf_path: Path, output_dir: Path, width: int, height: int) -> int:
    try:
        import fitz
    except ImportError as exc:
        raise RuntimeError(
            "Missing dependency: pymupdf. Install it with: pip install pymupdf"
        ) from exc

    if not pdf_path.exists():
        raise FileNotFoundError(f"PDF file not found: {pdf_path}")

    output_dir.mkdir(parents=True, exist_ok=True)

    doc = fitz.open(pdf_path)
    try:
        page_count = doc.page_count
        for index in range(page_count):
            page = doc.load_page(index)
            rect = page.rect
            scale_x = width / rect.width if rect.width else 1
            scale_y = height / rect.height if rect.height else 1
            scale = min(scale_x, scale_y)
            if not math.isfinite(scale) or scale <= 0:
                scale = 1
            matrix = fitz.Matrix(scale, scale)
            pix = page.get_pixmap(matrix=matrix, alpha=False)
            target = output_dir / f"slide_{index + 1:02d}.png"
            pix.save(str(target))
            print(f"Exported: {target}")
        return page_count
    finally:
        doc.close()


def open_presentation_with_fallback(powerpoint, pptx_path: Path):
    temp_dir = None
    try:
        presentation = powerpoint.Presentations.Open(
            str(pptx_path),
            ReadOnly=False,
            Untitled=False,
            WithWindow=False,
        )
        return presentation, temp_dir
    except Exception:
        temp_dir = Path(tempfile.mkdtemp(prefix="ppt_export_"))
        temp_pptx = temp_dir / "slides.pptx"
        shutil.copy2(pptx_path, temp_pptx)
        presentation = powerpoint.Presentations.Open(
            str(temp_pptx),
            ReadOnly=False,
            Untitled=False,
            WithWindow=False,
        )
        return presentation, temp_dir


def export_slides(pptx_path: Path, output_dir: Path, width: int, height: int) -> int:
    try:
        import pythoncom
        import win32com.client
    except ImportError as exc:
        raise RuntimeError(
            "Missing dependency: pywin32. Install it with: pip install pywin32"
        ) from exc

    if not pptx_path.exists():
        raise FileNotFoundError(f"PPT file not found: {pptx_path}")

    output_dir.mkdir(parents=True, exist_ok=True)

    pythoncom.CoInitialize()
    powerpoint = None
    presentation = None
    temp_dir = None
    try:
        powerpoint = win32com.client.DispatchEx("PowerPoint.Application")
        powerpoint.Visible = 1
        presentation, temp_dir = open_presentation_with_fallback(powerpoint, pptx_path)

        slide_count = presentation.Slides.Count
        for index in range(1, slide_count + 1):
            slide = presentation.Slides(index)
            target = output_dir / f"slide_{index:02d}.png"
            slide.Export(str(target), "PNG", width, height)
            print(f"Exported: {target}")

        return slide_count
    finally:
        if presentation is not None:
            presentation.Close()
        if powerpoint is not None:
            powerpoint.Quit()
        if temp_dir is not None and temp_dir.exists():
            shutil.rmtree(temp_dir, ignore_errors=True)
        pythoncom.CoUninitialize()


def export_pages(input_path: Path, output_dir: Path, width: int, height: int) -> int:
    suffix = input_path.suffix.lower()
    if suffix == ".pdf":
        return export_pdf_pages(input_path, output_dir, width, height)
    if suffix in {".ppt", ".pptx"}:
        return export_slides(input_path, output_dir, width, height)
    raise RuntimeError(f"Unsupported file type: {input_path.suffix}. Use .ppt, .pptx, or .pdf")


def main() -> int:
    args = parse_args()
    input_path = Path(args.input_path).expanduser().resolve()
    output_dir = build_output_dir(input_path, args.output_dir)

    try:
        count = export_pages(input_path, output_dir, args.width, args.height)
    except Exception as exc:
        print(f"Export failed: {exc}", file=sys.stderr)
        return 1

    print(f"Done. Exported {count} slides to: {output_dir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
