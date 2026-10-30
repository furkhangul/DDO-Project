import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";
import { QuoteTable, type Quote } from "../components/QuoteTable";
import { Badge, Card, Empty, Page, Query, useToast } from "../components/ui";
import { api } from "../lib/api";
import { addDays, dayShort, isoDay, tl } from "../lib/format";
import { StayFields, stayPayload, useBookingOptions, useQuoteAction, type StayForm } from "./NewReservation";

interface Row {
  id: number;
  code: string;
  check_in: string;
  check_out: string;
  status: string;
  type_code: string;
  total_net_kurus: number;
  total_tax_kurus: number;
}

export default function GuestHome() {
  const toast = useToast();
  const client = useQueryClient();
  const query = useQuery({ queryKey: ["my-stays"], queryFn: () => api<{ items: Row[] }>("/reservations?limit=50") });
  const { types, plans } = useBookingOptions();
  const [touched, setTouched] = useState<Partial<StayForm>>({});
  const [quote, setQuote] = useState<Quote | null>(null);
  const base = isoDay(new Date());

  const form: StayForm = {
    room_type_id: types.data?.items[0]?.id ?? 0,
    rate_plan_id: plans.data?.items[0]?.id ?? 0,
    check_in: addDays(base, 14),
    check_out: addDays(base, 16),
    adults: 2,
    children: "",
    codes: "",
    ...touched,
  };
  const price = useQuoteAction(form, setQuote);
  const book = useMutation({
    mutationFn: () => api("/reservations", { method: "POST", idempotent: true, body: { ...stayPayload(form), quote_hash: quote?.hash } }),
    onSuccess: () => {
      toast("Rezervasyonunuz alındı");
      setQuote(null);
      void client.invalidateQueries({ queryKey: ["my-stays"] });
    },
    onError: (error) => toast(error instanceof Error ? error.message : "Rezervasyon yapılamadı", true),
  });
  const selfCheckIn = useMutation({
    mutationFn: (id: number) => api(`/reservations/${id}/self-check-in`, { method: "POST", idempotent: true }),
    onSuccess: () => {
      toast("Girişiniz tamamlandı, iyi konaklamalar");
      void client.invalidateQueries({ queryKey: ["my-stays"] });
    },
    onError: (error) => toast(error instanceof Error ? error.message : "Giriş yapılamadı", true),
  });

  return (
    <Page title="Konaklamalarım">
      <div className="stack">
        <Card flush>
          <Query query={query}>
            {(data) =>
              data.items.length === 0 ? (
                <Empty title="Henüz rezervasyonunuz yok" />
              ) : (
                <ul className="rows">
                  {data.items.map((row) => (
                    <li key={row.id}>
                      <div className="row-line">
                        <span>
                          <Link to={`/rezervasyonlar/${row.id}`}>
                            <strong>
                              {dayShort(row.check_in)} – {dayShort(row.check_out)}
                            </strong>
                          </Link>
                          <span className="muted small">
                            {row.code} · {row.type_code} · {tl(row.total_net_kurus + row.total_tax_kurus)}
                          </span>
                        </span>
                        {row.status === "confirmed" ? (
                          <button className="btn primary" disabled={selfCheckIn.isPending} onClick={() => selfCheckIn.mutate(row.id)}>
                            Giriş yap
                          </button>
                        ) : (
                          <Badge status={row.status} />
                        )}
                      </div>
                    </li>
                  ))}
                </ul>
              )
            }
          </Query>
        </Card>
        {types.data && plans.data && (
          <Card title="Yeni konaklama">
            <StayFields
              form={form}
              types={types.data.items}
              plans={plans.data.items}
              set={(patch) => {
                setTouched({ ...touched, ...patch });
                setQuote(null);
              }}
            />
            {quote && <QuoteTable quote={quote} />}
            <div className="form-actions">
              {quote ? (
                <button className="btn primary" disabled={book.isPending} onClick={() => book.mutate()}>
                  {tl(quote.total_kurus)} ile onayla
                </button>
              ) : (
                <button className="btn primary" disabled={price.isPending} onClick={() => price.mutate()}>
                  Fiyatı gör
                </button>
              )}
            </div>
          </Card>
        )}
      </div>
    </Page>
  );
}
