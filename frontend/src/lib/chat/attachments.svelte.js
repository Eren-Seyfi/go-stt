// Chat attachments: type detection, upload, and the shared preview state.
import {AttachmentService} from '../../../bindings/changeme/backend';
import File_ from '@lucide/svelte/icons/file';
import FileArchive from '@lucide/svelte/icons/file-archive';
import FileCode from '@lucide/svelte/icons/file-code';
import FileImage from '@lucide/svelte/icons/file-image';
import FileMusic from '@lucide/svelte/icons/file-music';
import FileSpreadsheet from '@lucide/svelte/icons/file-spreadsheet';
import FileText from '@lucide/svelte/icons/file-text';
import FileType from '@lucide/svelte/icons/file-type';
import FileVideoCamera from '@lucide/svelte/icons/file-video-camera';

export const MAX_SIZE = 100 * 1024 * 1024; // must match the backend limit

/** @typedef {{id: string, name: string, mime: string, size: number}} Attachment */

const TEXT_EXT = /\.(txt|md|markdown|csv|tsv|json|jsonl|xml|ya?ml|toml|ini|cfg|conf|log|go|py|js|ts|jsx|tsx|svelte|vue|html?|css|scss|java|kt|c|h|cpp|hpp|cc|rs|rb|php|sh|bash|zsh|fish|sql|lua|dart|swift|cs|r|tex|srt|vtt)$/i;
const CODE_EXT = /\.(json|jsonl|xml|ya?ml|toml|go|py|js|ts|jsx|tsx|svelte|vue|html?|css|scss|java|kt|c|h|cpp|hpp|cc|rs|rb|php|sh|bash|zsh|fish|sql|lua|dart|swift|cs|r)$/i;

/** image | video | audio | pdf | text | code | sheet | doc | archive | file */
export function kindOf(a) {
  const m = a.mime || '';
  const n = a.name || '';
  if (/^image\/(png|jpe?g|webp|gif|avif|bmp|svg\+xml)$/.test(m)) return 'image';
  if (m.startsWith('video/')) return 'video';
  if (m.startsWith('audio/')) return 'audio';
  if (m === 'application/pdf') return 'pdf';
  if (/\.(xlsx?|ods|csv|tsv)$/i.test(n)) return 'sheet';
  if (CODE_EXT.test(n)) return 'code';
  if (m.startsWith('text/') || TEXT_EXT.test(n) || /json|xml|yaml/.test(m)) return 'text';
  if (/\.(docx?|odt|rtf|pptx?|odp)$/i.test(n)) return 'doc';
  if (/\.(zip|tar|gz|tgz|bz2|xz|7z|rar)$/i.test(n)) return 'archive';
  return 'file';
}

export const kindIcon = {
  image: FileImage, video: FileVideoCamera, audio: FileMusic, pdf: FileType, text: FileText,
  code: FileCode, sheet: FileSpreadsheet, doc: FileText, archive: FileArchive, file: File_,
};

export const kindLabel = {
  image: 'Resim', video: 'Video', audio: 'Ses', pdf: 'PDF', text: 'Metin', code: 'Kod',
  sheet: 'Tablo', doc: 'Belge', archive: 'Arşiv', file: 'Dosya',
};

/** Short type badge, e.g. "PDF", "DOCX", "MP4". */
export function extBadge(a) {
  const ext = (a.name || '').split('.').pop();
  return ext && ext !== a.name && ext.length <= 5 ? ext.toUpperCase() : kindLabel[kindOf(a)];
}

export function formatSize(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1).replace('.0', '')} MB`;
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

/** URL the app serves the stored file at. */
export const fileURL = (a) => `/attachments/${encodeURIComponent(a.id)}`;

function readBase64(file) {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result).replace(/^data:[^,]*,/, ''));
    r.onerror = () => reject(r.error ?? new Error('dosya okunamadı'));
    r.readAsDataURL(file);
  });
}

/** Stores a File in the app and returns its metadata. */
export async function uploadFile(file) {
  if (file.size > MAX_SIZE) throw new Error(`${file.name} çok büyük (en fazla ${formatSize(MAX_SIZE)})`);
  const data = await readBase64(file);
  return AttachmentService.Save(file.name || 'yapistirilan', file.type || '', data);
}

export const deleteFile = (a) => AttachmentService.Delete(a.id).catch(() => {});
export const openExternally = (a) => AttachmentService.Open(a.id);

// ---- preview ----------------------------------------------------------

/** The attachment preview shown over the window. */
export const preview = $state({items: /** @type {any[]} */ ([]), index: 0});

/** @param {any[]} items  attachments (or composer items with a local `url`) */
export function openPreview(items, index = 0) {
  preview.items = items;
  preview.index = index;
}

export function closePreview() {
  preview.items = [];
  preview.index = 0;
}
