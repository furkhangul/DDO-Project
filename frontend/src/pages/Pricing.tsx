import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Card, Empty, Field, Modal, Page, Query, useToast } from "../components/ui";
import { api } from "../lib/api";
import { can, useUser } from "../lib/auth";
import { dayShort, percent, tl, toKurus } from "../lib/format";

interface Overview {
  settings: { weekend_bp: number; child_free_max_age: number; child_max_age: number; kdv_bp: number; accommodation_bp: number };
  plans: { id: number; code: string; name: string; multiplier_bp: number; refundable: boolean }[];
  rates: { room_type_id: number; rate_plan_id: number; type_name: string; plan_name: string; plan_code: string; base_kurus: number; extra_adult_kurus: number; child_kurus: number }[];
  seasons: { id: number; name: string; date_from: string; date_to: string; multiplier_bp: number; min_stay: number }[];
  early_tiers: { id: number; min_lead_days: number; discount_bp: number }[];
  campaigns: { id: number; code: string; name: string; discount_bp: number; discount_fixed_kurus: number; valid_from: string; valid_to: string; min_nights: number; with_early_booking: boolean; used_count: number; max_uses: number | null; active: boolean }[];
  compatible: { code_a: string; code_b: string }[];
}

interface Suggestion {
  date: string;
  room_type: string;
  occupancy_pct: number;
  change_bp: number;
  reason: string;
}

export default function Pricing() {
  const user = useUser();
  const toast = useToast();
  const client = useQueryClient();
  const editable = can.price(user?.role);
  const [editing, setEditing] = useState<Overview["rates"][number] | null>(null);
  const query = useQuery({ queryKey: ["pricing"], queryFn: () => api<Overview>("/pricing") });
  const suggestions = useQuery({ queryKey: ["suggestions"], queryFn: () => api<{ items: Suggestion[] }>("/pricing/suggestions"), enabled: editable });
  const toggle = useMutation({
    mutationFn: ({ id, active }: { id: number; active: boolean }) => api(`/pricing/campaigns/${id}/active`, { method: "PUT", body: { active } }),
    onSuccess: () => void client.invalidateQueries({ queryKey: ["pricing"] }),
    onError: (error) => toast(error instanceof Error ? error.message : "Kampanya güncellenemedi", true),
  });

  return (
    <Page title="Fiyatlandırma" note="Yapılan değişiklikler yalnızca yeni rezervasyonları etkiler. Mevcut rezervasyonların fiyatı alındıkları anda sabitlenir.">
      <Query query={query}>
        {(data) => {
          const upcoming = data.seasons.filter((season) => season.date_to >= new Date().toISOString().slice(0, 10)).slice(0, 8);
          return (
            <div className="stack">
              <div className="stats">
                <div className="stat">
                  <span className="stat-label">Hafta sonu çarpanı</span>
                  <strong>{percent(data.settings.weekend_bp)}</strong>
                  <span className="muted small">Cuma ve cumartesi geceleri</span>
                </div>
                <div className="stat">
                  <span className="stat-label">KDV</span>
                  <strong>{percent(data.settings.kdv_bp)}</strong>
                </div>
                <div className="stat">
                  <span className="stat-label">Konaklama vergisi</span>
                  <strong>{percent(data.settings.accommodation_bp)}</strong>
                </div>
                <div className="stat">
                  <span className="stat-label">Çocuk</span>
                  <strong>{data.settings.child_free_max_age} yaş altı ücretsiz</strong>
                  <span className="muted small">{data.settings.child_max_age} yaşa kadar çocuk fiyatı</span>
                </div>
              </div>
              <Card title="Oda fiyatları" flush>
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>Oda tipi</th>
                        <th>Tarife</th>
                        <th className="num">Gecelik</th>
                        <th className="num">Ek yetişkin</th>
                        <th className="num">Çocuk</th>
                        {editable && <th />}
                      </tr>
                    </thead>
                    <tbody>
                      {data.rates.map((rate) => (
                        <tr key={`${rate.room_type_id}-${rate.rate_plan_id}`}>
                          <td>{rate.type_name}</td>
                          <td>{rate.plan_name}</td>
                          <td className="num">{tl(rate.base_kurus)}</td>
                          <td className="num">{tl(rate.extra_adult_kurus)}</td>
                          <td className="num">{tl(rate.child_kurus)}</td>
                          {editable && (
                            <td className="num">
                              <button className="link" onClick={() => setEditing(rate)}>
                                Düzenle
                              </button>
                            </td>
                          )}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </Card>
              <div className="grid-2">
                <Card title="Sezonlar" flush>
                  <ul className="rows">
                    {upcoming.map((season) => (
                      <li key={season.id}>
                        <div className="row-line">
                          <span>
                            <strong>{season.name}</strong>
                            <span className="muted small">
                              {dayShort(season.date_from)} – {dayShort(season.date_to)} · en az {season.min_stay} gece
                            </span>
                          </span>
                          <span>{percent(season.multiplier_bp)}</span>
                        </div>
                      </li>
                    ))}
                  </ul>
                </Card>
                <Card title="Erken rezervasyon" flush>
                  <ul className="rows">
                    {data.early_tiers.map((tier) => (
                      <li key={tier.id}>
                        <div className="row-line">
                          <span>{tier.min_lead_days} gün önceden</span>
                          <strong>{percent(tier.discount_bp)} indirim</strong>
                        </div>
                      </li>
                    ))}
                  </ul>
                </Card>
              </div>
              <Card title="Kampanyalar" flush>
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>Kod</th>
                        <th>Ad</th>
                        <th className="num">İndirim</th>
                        <th>En az</th>
                        <th>Erken ile</th>
                        <th>Kullanım</th>
                        <th>Durum</th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.campaigns.map((campaign) => (
                        <tr key={campaign.id}>
                          <td>
                            <strong>{campaign.code}</strong>
                          </td>
                          <td>{campaign.name}</td>
                          <td className="num">{campaign.discount_bp ? percent(campaign.discount_bp) : tl(campaign.discount_fixed_kurus)}</td>
                          <td>{campaign.min_nights} gece</td>
                          <td>{campaign.with_early_booking ? "Birleşir" : "Birleşmez"}</td>
                          <td>
                            {campaign.used_count}
                            {campaign.max_uses ? ` / ${campaign.max_uses}` : ""}
                          </td>
                          <td>
                            {editable ? (
                              <button className="link" disabled={toggle.isPending} onClick={() => toggle.mutate({ id: campaign.id, active: !campaign.active })}>
                                {campaign.active ? "Durdur" : "Başlat"}
                              </button>
                            ) : campaign.active ? (
                              "Etkin"
                            ) : (
                              "Durdu"
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <p className="muted small card-note">
                  Birlikte kullanılabilir: {data.compatible.map((pair) => `${pair.code_a} + ${pair.code_b}`).join(", ") || "tanımlı eşleşme yok"}
                </p>
              </Card>
              {editable && (
                <Card title="Doluluğa göre fiyat önerisi" flush>
                  <Query query={suggestions}>
                    {(result) => {
                      const items = result.items.filter((item) => item.change_bp !== 0).slice(0, 10);
                      return items.length === 0 ? (
                        <Empty title="Önümüzdeki 14 gün için öneri yok" />
                      ) : (
                        <ul className="rows">
                          {items.map((item) => (
                            <li key={`${item.date}-${item.room_type}`}>
                              <div className="row-line">
                                <span>
                                  <strong>
                                    {dayShort(item.date)} · {item.room_type}
                                  </strong>
                                  <span className="muted small">
                                    Doluluk %{item.occupancy_pct}. {item.reason}
                                  </span>
                                </span>
                                <strong>
                                  {item.change_bp > 0 ? "+" : "−"}
                                  {percent(Math.abs(item.change_bp))}
                                </strong>
                              </div>
                            </li>
                          ))}
                        </ul>
                      );
                    }}
                  </Query>
                </Card>
              )}
            </div>
          );
        }}
      </Query>
      {editing && <RateDialog rate={editing} onClose={() => setEditing(null)} />}
    </Page>
  );
}

function RateDialog({ rate, onClose }: { rate: Overview["rates"][number]; onClose: () => void }) {
  const toast = useToast();
  const client = useQueryClient();
  const format = (kurus: number) => (kurus / 100).toLocaleString("tr-TR", { minimumFractionDigits: 2 });
  const [base, setBase] = useState(format(rate.base_kurus));
  const save = useMutation({
    mutationFn: () =>
      api("/pricing/rates", {
        method: "PUT",
        body: { room_type_id: rate.room_type_id, rate_plan_id: rate.rate_plan_id, base_kurus: toKurus(base), extra_adult_kurus: rate.extra_adult_kurus, child_kurus: rate.child_kurus },
      }),
    onSuccess: () => {
      toast("Fiyat güncellendi");
      void client.invalidateQueries({ queryKey: ["pricing"] });
      onClose();
    },
    onError: (error) => toast(error instanceof Error ? error.message : "Kaydedilemedi", true),
  });
  return (
    <Modal title={`${rate.type_name} · ${rate.plan_name}`} onClose={onClose}>
      <Field label="Gecelik fiyat (₺)">{(id) => <input id={id} inputMode="decimal" value={base} onChange={(e) => setBase(e.target.value)} />}</Field>
      <div className="form-actions">
        <button className="btn primary" disabled={save.isPending} onClick={() => save.mutate()}>
          Kaydet
        </button>
      </div>
    </Modal>
  );
}
