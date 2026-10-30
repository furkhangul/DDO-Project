import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";
import { Card, Empty, Page, Query, useToast } from "../components/ui";
import { api } from "../lib/api";
import { dayShort, tl } from "../lib/format";

interface Overview {
  nights: { room_type_id: number; stay_date: string; capacity: number; overbook_limit: number; sold: number; type_code: string }[];
  candidates: { id: number; code: string; check_in: string; check_out: string; guest_name: string; type_code: string }[];
  partners: { id: number; name: string; distance_km: string; nightly_cost_kurus: number }[];
  walks: { id: number; code: string; guest_name: string; partner_name: string; nights: number; cost_kurus: number; reason: string }[];
}

export default function Overbooking() {
  const toast = useToast();
  const client = useQueryClient();
  const query = useQuery({ queryKey: ["overbooking"], queryFn: () => api<Overview>("/overbooking") });
  const [partner, setPartner] = useState<number>();
  const walk = useMutation({
    mutationFn: ({ id, target }: { id: number; target: number }) => api(`/reservations/${id}/walk`, { method: "POST", idempotent: true, body: { partner_hotel_id: target, reason: "Otel fazla satıldı" } }),
    onSuccess: () => {
      toast("Misafir yönlendirildi");
      void client.invalidateQueries({ queryKey: ["overbooking"] });
    },
    onError: (error) => toast(error instanceof Error ? error.message : "Yönlendirilemedi", true),
  });

  return (
    <Page title="Fazla Satış" note="Oda tipi başına tanımlı yüzde kadar kontrollü fazla satış yapılır. Oda sayısını aşan geceler burada listelenir.">
      <Query query={query}>
        {(data) => (
          <div className="stack">
            <Card title="Fazla satılan geceler" flush>
              {data.nights.length === 0 ? (
                <Empty title="Fazla satılan gece yok">Satışlar oda sayısının içinde.</Empty>
              ) : (
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>Gece</th>
                        <th>Tip</th>
                        <th className="num">Oda</th>
                        <th className="num">Satılan</th>
                        <th className="num">Fazla</th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.nights.map((night) => (
                        <tr key={`${night.room_type_id}-${night.stay_date}`}>
                          <td>{dayShort(night.stay_date)}</td>
                          <td>{night.type_code}</td>
                          <td className="num">{night.capacity}</td>
                          <td className="num">{night.sold}</td>
                          <td className="num">{night.sold - night.capacity}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </Card>
            <Card
              title="Yönlendirme adayları"
              aside={
                <select value={partner ?? data.partners[0]?.id} onChange={(e) => setPartner(Number(e.target.value))} aria-label="Anlaşmalı otel">
                  {data.partners.map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.name} ({tl(item.nightly_cost_kurus)}/gece)
                    </option>
                  ))}
                </select>
              }
              flush
            >
              {data.candidates.length === 0 ? (
                <Empty title="Aday yok" />
              ) : (
                <ul className="rows">
                  {data.candidates.map((row) => (
                    <li key={row.id}>
                      <div className="row-line">
                        <span>
                          <Link to={`/rezervasyonlar/${row.id}`}>
                            <strong>{row.guest_name}</strong>
                          </Link>
                          <span className="muted small">
                            {row.code} · {row.type_code} · {dayShort(row.check_in)} – {dayShort(row.check_out)}
                          </span>
                        </span>
                        <button className="btn" disabled={walk.isPending} onClick={() => walk.mutate({ id: row.id, target: partner ?? data.partners[0].id })}>
                          Yönlendir
                        </button>
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </Card>
            <Card title="Son yönlendirmeler" flush>
              {data.walks.length === 0 ? (
                <Empty title="Yönlendirme yapılmadı" />
              ) : (
                <ul className="rows">
                  {data.walks.map((row) => (
                    <li key={row.id}>
                      <div className="row-line">
                        <span>
                          <strong>{row.guest_name}</strong>
                          <span className="muted small">
                            {row.partner_name} · {row.nights} gece · {row.reason}
                          </span>
                        </span>
                        <span>{tl(row.cost_kurus)}</span>
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </Card>
          </div>
        )}
      </Query>
    </Page>
  );
}
