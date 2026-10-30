import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Badge, Card, Empty, Page, Query, Stat, useToast } from "../components/ui";
import { api } from "../lib/api";
import { can, useUser } from "../lib/auth";
import { dateTime, dayShort, statusLabels } from "../lib/format";

interface Overview {
  items: { id: number; code: string; name: string; commission_bp: number; active: boolean }[];
  inbox: { outcome: string; total: number }[];
  outbox: { status: string; total: number }[];
}

interface Inbox {
  id: number;
  event_id: string;
  external_ref: string;
  seq: number;
  event_type: string;
  outcome: string;
  detail: string;
  received_at: string;
  channel_code: string;
}

interface Outbox {
  id: number;
  channel_code: string;
  type_code: string;
  stay_date: string;
  status: string;
  attempts: number;
  last_error: string | null;
}

export default function Channels() {
  const user = useUser();
  const toast = useToast();
  const client = useQueryClient();
  const [status, setStatus] = useState("dead");
  const overview = useQuery({ queryKey: ["channels"], queryFn: () => api<Overview>("/channels"), refetchInterval: 5000 });
  const inbox = useQuery({ queryKey: ["inbox"], queryFn: () => api<{ items: Inbox[] }>("/channels/inbox?limit=25"), refetchInterval: 5000 });
  const outbox = useQuery({ queryKey: ["outbox", status], queryFn: () => api<{ items: Outbox[] }>(`/channels/outbox?limit=25&status=${status}`), refetchInterval: 5000 });
  const retry = useMutation({
    mutationFn: (id: number) => api(`/channels/outbox/${id}/retry`, { method: "POST" }),
    onSuccess: () => {
      toast("Mesaj yeniden kuyruğa alındı");
      void client.invalidateQueries({ queryKey: ["outbox"] });
    },
    onError: (error) => toast(error instanceof Error ? error.message : "Yeniden denenemedi", true),
  });

  const total = (rows: { total: number }[]) => rows.reduce((sum, row) => sum + row.total, 0);

  return (
    <Page title="Kanallar" note="Gelen bildirimler tekrar ve sıra kontrolünden geçer. Giden müsaitlik güncellemeleri kuyruktan iletilir, hata alırsa artan aralıklarla yeniden denenir.">
      <Query query={overview}>
        {(data) => (
          <div className="stats">
            <Stat label="Gelen olay" value={total(data.inbox).toLocaleString("tr-TR")} hint={data.inbox.map((row) => `${statusLabels[row.outcome]}: ${row.total}`).join(" · ")} />
            <Stat label="İletilen güncelleme" value={(data.outbox.find((row) => row.status === "sent")?.total ?? 0).toLocaleString("tr-TR")} />
            <Stat label="Bekleyen" value={data.outbox.filter((row) => row.status === "pending" || row.status === "sending").reduce((sum, row) => sum + row.total, 0)} />
            <Stat label="Ölü kuyruk" value={data.outbox.find((row) => row.status === "dead")?.total ?? 0} tone={data.outbox.some((row) => row.status === "dead" && row.total > 0) ? "bad" : undefined} />
          </div>
        )}
      </Query>
      <div className="grid-2">
        <Card title="Gelen olaylar" flush>
          <Query query={inbox}>
            {(data) =>
              data.items.length === 0 ? (
                <Empty title="Henüz olay gelmedi" />
              ) : (
                <ul className="rows">
                  {data.items.map((item) => (
                    <li key={item.id}>
                      <div className="row-line">
                        <span>
                          <strong>
                            {item.channel_code} · {item.external_ref}
                          </strong>
                          <span className="muted small">
                            {item.event_type} · sıra {item.seq} · {dateTime(item.received_at)}
                          </span>
                        </span>
                        <Badge status={item.outcome} />
                      </div>
                    </li>
                  ))}
                </ul>
              )
            }
          </Query>
        </Card>
        <Card
          title="Giden kuyruk"
          aside={
            <select value={status} onChange={(e) => setStatus(e.target.value)} aria-label="Kuyruk durumu">
              {["dead", "pending", "sent"].map((value) => (
                <option key={value} value={value}>
                  {statusLabels[value]}
                </option>
              ))}
            </select>
          }
          flush
        >
          <Query query={outbox}>
            {(data) =>
              data.items.length === 0 ? (
                <Empty title="Bu durumda mesaj yok" />
              ) : (
                <ul className="rows">
                  {data.items.map((item) => (
                    <li key={item.id}>
                      <div className="row-line">
                        <span>
                          <strong>
                            {item.channel_code} · {item.type_code} · {dayShort(item.stay_date)}
                          </strong>
                          <span className="muted small">
                            {item.attempts} deneme{item.last_error ? ` · ${item.last_error}` : ""}
                          </span>
                        </span>
                        {item.status === "dead" && can.price(user?.role) ? (
                          <button className="btn" disabled={retry.isPending} onClick={() => retry.mutate(item.id)}>
                            Yeniden dene
                          </button>
                        ) : (
                          <Badge status={item.status} />
                        )}
                      </div>
                    </li>
                  ))}
                </ul>
              )
            }
          </Query>
        </Card>
      </div>
    </Page>
  );
}
