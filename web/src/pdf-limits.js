export const pdfPreviewLimit=20*1024*1024;
export function pdfScale(width,height,zoom=1) {
 if(!Number.isFinite(width)||!Number.isFinite(height)||width<=0||height<=0)throw new Error('Некорректный размер страницы PDF');
 return Math.min(zoom,4096/width,4096/height,Math.sqrt(4*1024*1024/width/height));
}
