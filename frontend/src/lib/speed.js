// Formatting for model speed statistics.

const tr = (n, digits) => n.toFixed(digits).replace('.', ',');

/** Tokens per second, e.g. "38,4" or "120". */
export const fmtTps = (n) => (!n ? '–' : n >= 100 ? String(Math.round(n)) : tr(n, 1));

/** Duration, e.g. "420 ms" or "3,2 sn". */
export const fmtMs = (ms) => (ms < 1000 ? `${Math.round(ms)} ms` : `${tr(ms / 1000, 1)} sn`);

/** Tooltip with every detail of a reply's stats. */
export function statsTitle(st) {
  const lines = [
    `Üretilen: ${st.outputTokens}${st.approximate ? ' (tahmini)' : ''} token`,
    `Hız: ${fmtTps(st.tokensPerSec)} token/sn`,
    `İlk kelimeye kadar: ${fmtMs(st.firstTokenMs)}`,
    `Toplam: ${fmtMs(st.totalMs)}`,
  ];
  if (st.inputTokens) lines.splice(1, 0, `İstem: ${st.inputTokens} token${st.promptPerSec ? ` (${fmtTps(st.promptPerSec)} token/sn)` : ''}`);
  lines.push(st.exact ? 'Ölçüm: model sunucusundan (kesin)' : 'Ölçüm: uygulama tarafından (yaklaşık)');
  return lines.join('\n');
}
