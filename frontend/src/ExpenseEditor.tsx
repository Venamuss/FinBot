import { useEffect, useState } from "react";
import { api, errorMessage } from "./api";
import type { Expense, Member } from "./api";
import Dialog from "./Dialog";
import ExpenseForm from "./ExpenseForm";

export default function ExpenseEditor({
  id,
  members,
  onClose,
  onSaved,
}: {
  id: string | null;
  members: Member[];
  onClose: () => void;
  onSaved: (message: string) => void;
}) {
  const [original, setOriginal] = useState<Expense>();
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!id) return;
    const controller = new AbortController();
    setError("");
    setOriginal(undefined);
    api
      .get(id, controller.signal)
      .then((item) => {
        if (!controller.signal.aborted) setOriginal(item);
      })
      .catch((error) => {
        if (!controller.signal.aborted) setError(errorMessage(error));
      });
    return () => controller.abort();
  }, [id, attempt]);

  return (
    <Dialog
      title={id ? "Редактировать расход" : "Новый расход"}
      busy={busy}
      onClose={onClose}
    >
      {error ? (
        <div className="notice error" role="alert">
          <p>{error}</p>
          <button onClick={() => setAttempt((value) => value + 1)}>
            Повторить загрузку
          </button>
        </div>
      ) : id && !original ? (
        <p role="status">Загружаем расход…</p>
      ) : (
        <ExpenseForm
          original={original}
          members={members}
          onSaved={onSaved}
          onCancel={onClose}
          onBusy={setBusy}
        />
      )}
    </Dialog>
  );
}
