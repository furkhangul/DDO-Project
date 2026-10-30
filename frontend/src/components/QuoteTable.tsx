import { dayName, dayShort, percent, tl } from "../lib/format";

export interface QuoteNight {
  date: string;
  season: string;
  weekend: boolean;
  gross_kurus: number;
  early_discount_kurus: number;
  campaign_discount_kurus: number;
  net_kurus: number;
  kdv_kurus: number;
  accommodation_tax_kurus: number;
  total_kurus: number;
}

export interface Quote {
  nights: QuoteNight[];
  gross_kurus: number;
  discount_kurus: number;
  net_kurus: number;
  kdv_kurus: number;
  accommodation_tax_kurus: number;
  total_kurus: number;
  early_discount_bp: number;
  applied_codes: string[];
  notes: string[];
  hash: string;
}

export function QuoteTable({ quote }: { quote: Quote }) {
  return (
    <>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Gece</th>
              <th>Dönem</th>
              <th className="num">Brüt</th>
              <th className="num">İndirim</th>
              <th className="num">Net</th>
              <th className="num">KDV</th>
              <th className="num">Konaklama v.</th>
              <th className="num">Toplam</th>
            </tr>
          </thead>
          <tbody>
            {quote.nights.map((night) => (
              <tr key={night.date}>
                <td className="nowrap">
                  {dayName(night.date)} {dayShort(night.date)}
                </td>
                <td>
                  {night.season || "Standart"}
                  {night.weekend && " · Hafta sonu"}
                </td>
                <td className="num">{tl(night.gross_kurus)}</td>
                <td className="num">{tl(night.early_discount_kurus + night.campaign_discount_kurus)}</td>
                <td className="num">{tl(night.net_kurus)}</td>
                <td className="num">{tl(night.kdv_kurus)}</td>
                <td className="num">{tl(night.accommodation_tax_kurus)}</td>
                <td className="num">{tl(night.total_kurus)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="muted small quote-notes">
        {quote.early_discount_bp > 0 && `Erken rezervasyon ${percent(quote.early_discount_bp)}. `}
        {quote.applied_codes.length > 0 && `Kampanya: ${quote.applied_codes.join(", ")}. `}
        {quote.notes.join(" ")}
      </p>
    </>
  );
}
