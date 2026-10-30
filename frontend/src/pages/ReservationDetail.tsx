import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { FolioPanel } from "../components/FolioPanel";
import { Badge, Card, Field, Modal, Page, Query, Stat, useToast } from "../components/ui";
import { api } from "../lib/api";
import { can, useUser } from "../lib/auth";
import { dayLong, dayShort, diffDays, tl, viewLabels } from "../lib/format";

interface Detail {
  id: number;
  code: string;
  status: string;
  guest_name: string;
  type_name: string;
  plan_name: string;
  check_in: string;
  check_out: string;
  adults: number;
  children_ages: number[];
  channel_code: string | null;
  external_ref: string | null;
  pref_floor: number | null;
  pref_view: string | null;
  adjacent_group: string | null;
  booked_on: string;
  total_net_kurus: number;
  total_tax_kurus: number;
  price_hash: string;
  nights: { stay_date: string; net_kurus: number; kdv_kurus: number; accom_kurus: number }[];
  assignments: { id: number; room_number: string; date_from: string; date_to: string; locked: boolean }[];
}

export default function ReservationDetail() {
  const { id } = useParams();
  const user = useUser();
  const client = useQueryClient();
  const toast = useToast();
  const [walking, setWalking] = useState(false);
  const query = useQuery({ queryKey: ["reservation", id], queryFn: () => api<Detail>(`/reservations/${id}`) });
  const price = useQuery({
    queryKey: ["reservation-price", id],
    queryFn: () => api<{ matches: boolean; replayed: unknown }>(`/reservations/${id}/price`),
  });

  const action = useMutation({
    mutationFn: ({ path, label }: { path: string; label: string }) => api(`/reservations/${id}/${path}`, { method: "POST", idempotent: true }).then(() => label),
    onSuccess: (label) => {
      toast(label);
      void client.invalidateQueries({ queryKey: ["reservation", id] });
      void client.invalidateQueries({ queryKey: ["folios", id] });
    },
    onError: (error) => toast(error instanceof Error ? error.message : "İşlem yapılamadı", true),
  });

  return (
    <Query query={query}>
      {(r) => (
        <Page
          title={r.guest_name}
          note={
            <>
              {r.code} <Badge status={r.status} />
            </>
          }
          actions={
            <div className="toolbar">
              {can.desk(user?.role) && r.status === "confirmed" && (
                <button className="btn primary" disabled={action.isPending} onClick={() => action.mutate({ path: "check-in", label: "Giriş yapıldı" })}>
                  Giriş yap
                </button>
              )}
              {can.desk(user?.role) && r.status === "checked_in" && (
                <button className="btn primary" disabled={action.isPending} onClick={() => action.mutate({ path: "check-out", label: "Çıkış yapıldı" })}>
                  Çıkış yap
                </button>
              )}
              {can.desk(user?.role) && r.status === "confirmed" && (
                <button className="btn" onClick={() => setWalking(true)}>
                  Yönlendir
                </button>
              )}
              {r.status === "confirmed" && user?.role !== "revenue" && (
                <button className="btn danger" disabled={action.isPending} onClick={() => window.confirm("Rezervasyon iptal edilsin mi?") && action.mutate({ path: "cancel", label: "Rezervasyon iptal edildi" })}>
                  İptal et
                </button>
              )}
            </div>
          }
        >
          <div className="stats">
            <Stat label="Giriş" value={dayShort(r.check_in)} hint={dayLong(r.check_in)} />
            <Stat label="Çıkış" value={dayShort(r.check_out)} hint={`${diffDays(r.check_in, r.check_out)} gece`} />
            <Stat label="Toplam" value={tl(r.total_net_kurus + r.total_tax_kurus)} hint={`${tl(r.total_net_kurus)} + vergi`} />
            <Stat
              label="Fiyat garantisi"
              value={price.data?.replayed ? (price.data.matches ? "Doğrulandı" : "Uyuşmuyor") : "Sabit tutar"}
              tone={price.data?.replayed && !price.data.matches ? "bad" : undefined}
              hint={price.data?.replayed ? "Anlık kurallarla yeniden hesaplandı" : "Kanal veya arşiv fiyatı"}
            />
          </div>
          <div className="grid-2">
            <Card title="Konaklama">
              <dl className="facts">
                <dt>Oda tipi</dt>
                <dd>{r.type_name}</dd>
                <dt>Tarife</dt>
                <dd>{r.plan_name}</dd>
                <dt>Kişi</dt>
                <dd>
                  {r.adults} yetişkin{r.children_ages.length > 0 && `, ${r.children_ages.length} çocuk`}
                </dd>
                <dt>Kaynak</dt>
                <dd>{r.channel_code ? `${r.channel_code} (${r.external_ref})` : "Direkt"}</dd>
                <dt>İstekler</dt>
                <dd>
                  {[r.pref_floor && `${r.pref_floor}. kat`, r.pref_view && `${viewLabels[r.pref_view]} manzara`, r.adjacent_group && `Bitişik: ${r.adjacent_group}`].filter(Boolean).join(", ") || "Yok"}
                </dd>
              </dl>
            </Card>
            <Card title="Oda" aside={can.book(user?.role) && <Link to="/cizelge">Çizelgede aç</Link>}>
              {r.assignments.length === 0 ? (
                <p className="muted">Henüz oda atanmadı. Girişte hazır bir oda seçilir.</p>
              ) : (
                <ul className="segments">
                  {r.assignments.map((segment) => (
                    <li key={segment.id}>
                      <span className="room-tag">{segment.room_number}</span>
                      {dayShort(segment.date_from)} – {dayShort(segment.date_to)}
                      {segment.locked && <span className="muted small"> kilitli</span>}
                    </li>
                  ))}
                </ul>
              )}
            </Card>
          </div>
          <FolioPanel reservationId={r.id} />
          {walking && <WalkDialog id={r.id} onClose={() => setWalking(false)} />}
        </Page>
      )}
    </Query>
  );
}

function WalkDialog({ id, onClose }: { id: number; onClose: () => void }) {
  const toast = useToast();
  const client = useQueryClient();
  const [partner, setPartner] = useState<number>();
  const [reason, setReason] = useState("Otel fazla satıldı");
  const options = useQuery({ queryKey: ["overbooking"], queryFn: () => api<{ partners: { id: number; name: string; distance_km: string }[] }>("/overbooking") });
  const walk = useMutation({
    mutationFn: () => api(`/reservations/${id}/walk`, { method: "POST", idempotent: true, body: { partner_hotel_id: partner ?? options.data?.partners[0].id, reason } }),
    onSuccess: () => {
      toast("Misafir yönlendirildi");
      void client.invalidateQueries();
      onClose();
    },
    onError: (error) => toast(error instanceof Error ? error.message : "Yönlendirilemedi", true),
  });
  return (
    <Modal title="Misafiri yönlendir" onClose={onClose}>
      <Field label="Anlaşmalı otel">
        {(fieldId) => (
          <select id={fieldId} value={partner} onChange={(e) => setPartner(Number(e.target.value))}>
            {options.data?.partners.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name}
              </option>
            ))}
          </select>
        )}
      </Field>
      <Field label="Neden">{(fieldId) => <input id={fieldId} value={reason} onChange={(e) => setReason(e.target.value)} />}</Field>
      <div className="form-actions">
        <button className="btn primary" disabled={walk.isPending} onClick={() => walk.mutate()}>
          Yönlendir
        </button>
      </div>
    </Modal>
  );
}
