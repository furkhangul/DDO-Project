import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Badge, Card, Empty, Page, Query, Stat, useToast } from "../components/ui";
import { api } from "../lib/api";
import { dateTime, dayLong, dayShort } from "../lib/format";

interface Run {
  business_date: string;
  status: string;
  total_items: number;
  done_items: number;
  attempts: number;
  last_error: string | null;
  finished_at: string | null;
}

interface Status {
  business_date: string;
  current: Run | null;
  history: Run[];
  integrity_violations: number;
  duplicate_charges: number;
}

export default function NightAudit() {
  const toast = useToast();
  const client = useQueryClient();
  const query = useQuery({ queryKey: ["night-audit"], queryFn: () => api<Status>("/night-audit") });
  const run = useMutation({
    mutationFn: () => api<{ charged: number; no_shows: number; next_business_date: string; resumed: boolean }>("/night-audit/run", { method: "POST" }),
    onSuccess: (report) => {
      toast(`${report.charged} konaklamaya ücret yazıldı, ${report.no_shows} gelmedi. Yeni gün: ${dayShort(report.next_business_date)}`);
      void client.invalidateQueries();
    },
    onError: (error) => {
      toast(error instanceof Error ? error.message : "Denetim tamamlanamadı", true);
      void client.invalidateQueries({ queryKey: ["night-audit"] });
    },
  });

  return (
    <Page
      title="Gece Denetimi"
      note={query.data && `Kapanacak gün: ${dayLong(query.data.business_date)}`}
      actions={
        <button className="btn primary" disabled={run.isPending} onClick={() => window.confirm("Otel günü kapatılsın mı?") && run.mutate()}>
          {run.isPending ? "Çalışıyor…" : query.data?.current?.status === "running" ? "Kaldığı yerden sürdür" : "Denetimi başlat"}
        </button>
      }
    >
      <Query query={query}>
        {(data) => (
          <div className="stack">
            <div className="stats">
              <Stat label="Bütünlük ihlali" value={data.integrity_violations} tone={data.integrity_violations ? "bad" : undefined} hint="Folyo bakiyesi ≠ kalem toplamı" />
              <Stat label="Çift ücret" value={data.duplicate_charges} tone={data.duplicate_charges ? "bad" : undefined} hint="Aynı gece iki kez yazılan ücret" />
              {data.current && <Stat label="Süren denetim" value={`${data.current.done_items}/${data.current.total_items}`} tone="warn" hint={data.current.last_error ?? `${data.current.attempts}. deneme`} />}
            </div>
            <Card title="Geçmiş denetimler" flush>
              {data.history.length === 0 ? (
                <Empty title="Henüz denetim çalışmadı" />
              ) : (
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>Otel günü</th>
                        <th>Durum</th>
                        <th className="num">Konaklama</th>
                        <th className="num">Deneme</th>
                        <th>Bitiş</th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.history.map((item) => (
                        <tr key={item.business_date}>
                          <td>{dayShort(item.business_date)}</td>
                          <td>
                            <Badge status={item.status} />
                          </td>
                          <td className="num">
                            {item.done_items}/{item.total_items}
                          </td>
                          <td className="num">{item.attempts}</td>
                          <td>{item.finished_at ? dateTime(item.finished_at) : "–"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </Card>
          </div>
        )}
      </Query>
    </Page>
  );
}
