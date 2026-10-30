const money = new Intl.NumberFormat("tr-TR", { style: "currency", currency: "TRY", maximumFractionDigits: 2 });
const compact = new Intl.NumberFormat("tr-TR", { style: "currency", currency: "TRY", maximumFractionDigits: 0 });

export const tl = (kurus: number) => money.format(kurus / 100);
export const tlShort = (kurus: number) => compact.format(kurus / 100);
export const percent = (bp: number) => `%${(bp / 100).toLocaleString("tr-TR")}`;

export function toKurus(value: string) {
  const parsed = Number(value.replace(/\./g, "").replace(",", "."));
  return Number.isFinite(parsed) ? Math.round(parsed * 100) : NaN;
}

const parts = (iso: string) => iso.slice(0, 10).split("-").map(Number);

export function parseDay(iso: string) {
  const [year, month, day] = parts(iso);
  return new Date(Date.UTC(year, month - 1, day));
}

export const isoDay = (date: Date) => date.toISOString().slice(0, 10);

export function addDays(iso: string, days: number) {
  const date = parseDay(iso);
  date.setUTCDate(date.getUTCDate() + days);
  return isoDay(date);
}

export const diffDays = (from: string, to: string) => Math.round((parseDay(to).getTime() - parseDay(from).getTime()) / 86400000);

const short = new Intl.DateTimeFormat("tr-TR", { day: "numeric", month: "short", timeZone: "UTC" });
const long = new Intl.DateTimeFormat("tr-TR", { day: "numeric", month: "long", year: "numeric", timeZone: "UTC" });
const weekday = new Intl.DateTimeFormat("tr-TR", { weekday: "short", timeZone: "UTC" });
const stamp = new Intl.DateTimeFormat("tr-TR", { dateStyle: "short", timeStyle: "medium" });

export const dayShort = (iso: string) => short.format(parseDay(iso));
export const dayLong = (iso: string) => long.format(parseDay(iso));
export const dayName = (iso: string) => weekday.format(parseDay(iso));
export const dateTime = (iso: string) => stamp.format(new Date(iso));

export const statusLabels: Record<string, string> = {
  confirmed: "Onaylı",
  checked_in: "Konaklıyor",
  checked_out: "Çıkış yaptı",
  cancelled: "İptal",
  no_show: "Gelmedi",
  walked: "Yönlendirildi",
  dirty: "Kirli",
  clean: "Temiz",
  inspected: "Kontrol edildi",
  out_of_order: "Arızalı",
  bekliyor: "Bekliyor",
  devam: "Devam ediyor",
  tamamlandi: "Tamamlandı",
  applied: "İşlendi",
  stale: "Eski sıra",
  conflict: "Çakışma",
  rejected: "Reddedildi",
  pending: "Bekliyor",
  sending: "Gönderiliyor",
  sent: "İletildi",
  dead: "Ölü kuyruk",
  superseded: "Yenisi var",
  running: "Sürüyor",
  completed: "Tamamlandı",
};

export const taskLabels: Record<string, string> = {
  cikis_temizligi: "Çıkış temizliği",
  konaklama_temizligi: "Konaklama temizliği",
  kontrol: "Kontrol",
  bakim: "Bakım",
};

export const viewLabels: Record<string, string> = { deniz: "Deniz", bahce: "Bahçe", sehir: "Şehir" };

export const roleLabels: Record<string, string> = {
  reception: "Resepsiyon",
  reservations: "Rezervasyon",
  housekeeping: "Kat Hizmetleri",
  revenue: "Gelir Yönetimi",
  manager: "Genel Müdür",
  guest: "Misafir",
};
