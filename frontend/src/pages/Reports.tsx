import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { BarList, LineChart } from "../components/Charts";
import { Card, Page, Query, Stat } from "../components/ui";
import { api } from "../lib/api";
import { addDays, dayShort, tl, tlShort } from "../lib/format";

interface Point {
  date: string;
  rooms_available: number;
  rooms_sold: number;
  room_revenue_kurus: number;
  occupancy_pct: number;
  adr_kurus: number;
  revpar_kurus: number;
  on_the_books: boolean;
}

interface Report {
  points: Point[];
  summary: Point;
  by_type: { code: string; name: string; rooms_sold: number; room_revenue_kurus: number }[];
}

const ranges = [
  { label: "Son 30 gün", days: 30, month: false },
  { label: "Son 90 gün", days: 90, month: false },
  { label: "Son 1 yıl", days: 365, month: true },
  { label: "Son 3 yıl", days: 1095, month: true },
];

const TEAL = "#008c7a";
const AMBER = "#c0761c";

export default function Reports() {
  const [range, setRange] = useState(ranges[1]);
  const [table, setTable] = useState(false);
  const today = useQuery({ queryKey: ["dashboard"], queryFn: () => api<{ business_date: string }>("/dashboard") });
  const end = today.data?.business_date;
  const query = useQuery({
    queryKey: ["kpi", end, range.days],
    enabled: Boolean(end),
    queryFn: () => api<Report>(`/reports/kpi?from=${addDays(end!, -range.days)}&to=${end}${range.month ? "&bucket=month" : ""}`),
  });

  return (
    <Page
      title="Raporlar"
      actions={
        <div className="toolbar">
          {ranges.map((item) => (
            <button key={item.days} className={item === range ? "chip on" : "chip"} onClick={() => setRange(item)}>
              {item.label}
            </button>
          ))}
        </div>
      }
    >
      <Query query={{ ...query, isPending: query.isPending || today.isPending }}>
        {(data) => {
          const labels = data.points.map((point) => (point.date.length === 7 ? point.date : dayShort(point.date)));
          return (
            <div className="stack">
              <div className="stats">
                <Stat label="Doluluk" value={`%${data.summary.occupancy_pct.toLocaleString("tr-TR")}`} hint={`${data.summary.rooms_sold.toLocaleString("tr-TR")} oda gecesi`} />
                <Stat label="ADR" value={tl(data.summary.adr_kurus)} hint="Satılan oda başına gelir" />
                <Stat label="RevPAR" value={tl(data.summary.revpar_kurus)} hint="Müsait oda başına gelir" />
                <Stat label="Oda geliri" value={tlShort(data.summary.room_revenue_kurus)} hint="Vergiler hariç" />
              </div>
              <Card title="Doluluk oranı" aside={<button className="link" onClick={() => setTable(!table)}>{table ? "Grafik" : "Tablo"}</button>}>
                {table ? (
                  <div className="table-wrap tall">
                    <table>
                      <thead>
                        <tr>
                          <th>Dönem</th>
                          <th className="num">Doluluk</th>
                          <th className="num">ADR</th>
                          <th className="num">RevPAR</th>
                          <th className="num">Gelir</th>
                        </tr>
                      </thead>
                      <tbody>
                        {data.points.map((point, index) => (
                          <tr key={point.date}>
                            <td>{labels[index]}</td>
                            <td className="num">%{point.occupancy_pct.toLocaleString("tr-TR")}</td>
                            <td className="num">{tl(point.adr_kurus)}</td>
                            <td className="num">{tl(point.revpar_kurus)}</td>
                            <td className="num">{tlShort(point.room_revenue_kurus)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <LineChart
                    label="Dönemlere göre doluluk yüzdesi"
                    labels={labels}
                    series={[{ name: "Doluluk", color: TEAL, values: data.points.map((point) => point.occupancy_pct) }]}
                    format={(value) => `%${Math.round(value)}`}
                    max={100}
                  />
                )}
              </Card>
              <div className="grid-2">
                <Card title="ADR ve RevPAR">
                  <LineChart
                    label="Dönemlere göre ADR ve RevPAR"
                    labels={labels}
                    series={[
                      { name: "ADR", color: TEAL, values: data.points.map((point) => point.adr_kurus) },
                      { name: "RevPAR", color: AMBER, values: data.points.map((point) => point.revpar_kurus) },
                    ]}
                    format={tl}
                    axisFormat={tlShort}
                  />
                </Card>
                <Card title="Oda tipine göre gelir">
                  <BarList rows={data.by_type.map((row) => ({ label: row.name, value: row.room_revenue_kurus, hint: `${row.rooms_sold} oda gecesi` }))} format={tlShort} />
                </Card>
              </div>
            </div>
          );
        }}
      </Query>
    </Page>
  );
}
