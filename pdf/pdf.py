import fitz
import os

def pdf_to_images(pdf_path, output_folder):
    # 创建输出文件夹
    os.makedirs(output_folder, exist_ok=True)
    # 打开PDF
    doc = fitz.open(pdf_path)
    # 遍历每一页
    for page_num in range(len(doc)):
        page = doc.load_page(page_num)
        # 渲染为图片（可调整分辨率，dpi=300更清晰）
        pix = page.get_pixmap(dpi=300)
        # 保存图片，命名为page_001.png、page_002.png...
        output_path = os.path.join(output_folder, f"page_{page_num+1:03d}.png")
        pix.save(output_path)
    doc.close()
    print(f"已完成！图片保存在{output_folder}")

# 调用函数
pdf_to_images("01-计算机类14组需求分析报告.pdf", "output_images_folder_1")
pdf_to_images("02-计算机类14组概要设计报告.pdf", "output_images_folder_2")