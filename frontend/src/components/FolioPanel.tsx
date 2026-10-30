import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../lib/api";
import { can, useUser } from "../lib/auth";
import { dayShort, tl, toKurus } from "../lib/format";
import { Card, Empty, Field, Modal, Query, useToast } from "./ui";

interface Line {
  id: number;
  kind: string;
  amount_kurus: number;
  description: string;
  business_date: string;
  posted_by_name: string | null;
}

interface FolioView {
  folio: { id: number; payer_name: string; payer_type: string; balance_kurus: number; is_primary: boolean; closed_at: string | null };
  lines: Line[];
}

type Dialog = { kind: "charge" | "payment" | "split"; folio: number } | { kind: "refund"; folio: number; line: Line } | null;

export function FolioPanel({ reservationId }: { reservationId: number }) {
  const user = useUser();
  const [dialog, setDialog] = useState<Dialog>(null);
  const query = useQuery({ queryKey: ["folios", String(reservationId)], queryFn: () => api<{ items: FolioView[] }>(`/reservations/${reservationId}/folios`) });
  const editable = can.folio(user?.role);
  return (
    <Query query={query}>
      {(data) =>
        data.items.length === 0 ? (
          <Card title="Folyo">
            <Empty title="Bu kayıt için folyo yok">Geçmiş dönem kayıtlarında folyo arşivlenmemiştir.</Empty>
          </Card>
        ) : (
          <>
            {data.items.map(({ folio, lines }) => (
              <Card
                key={folio.id}
                title={`Folyo: ${folio.payer_name}${folio.payer_type === "company" ? " (şirket)" : ""}`}
                aside={<strong className={folio.balance_kurus > 0 ? "balance due" : "balance"}>{tl(folio.balance_kurus)}</strong>}
                flush
              >
                {lines.length === 0 ? (
                  <Empty title="Henüz hareket yok">Oda ücreti ve vergiler gece denetiminde yazılır.</Empty>
                ) : (
                  <div className="table-wrap">
                    <table>
                      <thead>
                        <tr>
                          <th>Tarih</th>
                          <th>Açıklama</th>
                          <th className="num">Tutar</th>
                          {editable && <th />}
                        </tr>
                      </thead>
                      <tbody>
                        {lines.map((line) => (
                          <tr key={line.id}>
                            <td className="nowrap">{dayShort(line.business_date)}</td>
                            <td>{line.description}</td>
                            <td className="num">{tl(line.amount_kurus)}</td>
                            {editable && (
                              <td className="num">
                                {line.kind === "payment" && !folio.closed_at && (
                                  <button className="link" onClick={() => setDialog({ kind: "refund", folio: folio.id, line })}>
                                    İade
                                  </button>
                                )}
                              </td>
                            )}
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
                {editable && !folio.closed_at && (
                  <div className="card-actions">
                    <button className="btn" onClick={() => setDialog({ kind: "charge", folio: folio.id })}>
                      Harcama ekle
                    </button>
                    <button className="btn" onClick={() => setDialog({ kind: "payment", folio: folio.id })}>
                      Ödeme al
                    </button>
                    <button className="btn" onClick={() => setDialog({ kind: "split", folio: folio.id })}>
                      Böl
                    </button>
                  </div>
                )}
              </Card>
            ))}
            {dialog && <FolioDialog dialog={dialog} reservationId={reservationId} onClose={() => setDialog(null)} />}
          </>
        )
      }
    </Query>
  );
}

function FolioDialog({ dialog, reservationId, onClose }: { dialog: NonNullable<Dialog>; reservationId: number; onClose: () => void }) {
  const toast = useToast();
  const client = useQueryClient();
  const [amount, setAmount] = useState("");
  const [text, setText] = useState("");
  const [method, setMethod] = useState("kart");
  const [percent, setPercent] = useState("");

  const titles = { charge: "Harcama ekle", payment: "Ödeme al", split: "Folyoyu böl", refund: "İade yap" };
  const submit = useMutation({
    mutationFn: () => {
      const base = `/folios/${dialog.folio}`;
      const kurus = toKurus(amount);
      if (dialog.kind !== "split" && !(kurus > 0)) throw new Error("Geçerli bir tutar girin");
      switch (dialog.kind) {
        case "charge":
          return api(`${base}/charges`, { method: "POST", idempotent: true, body: { amount_kurus: kurus, description: text } });
        case "payment":
          return api(`${base}/payments`, { method: "POST", idempotent: true, body: { amount_kurus: kurus, method } });
        case "refund":
          return api(`${base}/refunds`, { method: "POST", idempotent: true, body: { payment_line_id: dialog.line.id, amount_kurus: kurus } });
        case "split":
          return api(`${base}/split`, {
            method: "POST",
            idempotent: true,
            body: { payer_type: "company", payer_name: text, percent: Number(percent) || 0, amount_kurus: percent ? 0 : kurus || 0 },
          });
      }
    },
    onSuccess: () => {
      toast("İşlem kaydedildi");
      void client.invalidateQueries({ queryKey: ["folios", String(reservationId)] });
      onClose();
    },
    onError: (error) => toast(error instanceof Error ? error.message : "İşlem yapılamadı", true),
  });

  return (
    <Modal title={titles[dialog.kind]} onClose={onClose}>
      <form
        className="stack"
        onSubmit={(event) => {
          event.preventDefault();
          submit.mutate();
        }}
      >
        {(dialog.kind === "charge" || dialog.kind === "split") && (
          <Field label={dialog.kind === "split" ? "Şirket adı" : "Açıklama"}>{(id) => <input id={id} required value={text} onChange={(e) => setText(e.target.value)} />}</Field>
        )}
        {dialog.kind === "split" && (
          <Field label="Yüzde" hint="Yüzde girerseniz tutar yok sayılır">
            {(id) => <input id={id} type="number" min={1} max={100} value={percent} onChange={(e) => setPercent(e.target.value)} />}
          </Field>
        )}
        <Field label="Tutar (₺)">{(id) => <input id={id} inputMode="decimal" placeholder="1.250,00" value={amount} onChange={(e) => setAmount(e.target.value)} />}</Field>
        {dialog.kind === "payment" && (
          <Field label="Yöntem">
            {(id) => (
              <select id={id} value={method} onChange={(e) => setMethod(e.target.value)}>
                <option value="kart">Kart</option>
                <option value="nakit">Nakit</option>
                <option value="havale">Havale</option>
              </select>
            )}
          </Field>
        )}
        <div className="form-actions">
          <button className="btn primary" disabled={submit.isPending}>
            Kaydet
          </button>
        </div>
      </form>
    </Modal>
  );
}
