import { useState } from "react";
import { api, errorMessage } from "./api";
import type { Expense } from "./api";
import Dialog from "./Dialog";
import { displayAmount, displayDate } from "./validation";

export default function DeleteExpense({
  expense,
  onClose,
  onDeleted,
}: {
  expense: Expense;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function remove() {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await api.delete(expense.id);
      onDeleted();
    } catch (error) {
      setError(errorMessage(error));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog title="Удалить расход?" busy={busy} onClose={onClose}>
      <p>
        <strong>{displayAmount(expense.amount)}</strong> · {expense.category} ·{" "}
        {displayDate(expense.spent_at)}
      </p>
      <p className="muted">
        Участник: {expense.user_id}. Восстановить запись после удаления нельзя.
      </p>
      {error && (
        <p className="notice error" role="alert">
          {error}
        </p>
      )}
      <div className="dialog-actions">
        <button onClick={onClose} disabled={busy} data-autofocus>
          Отмена
        </button>
        <button className="primary" onClick={remove} disabled={busy}>
          {busy ? "Удаляем…" : "Удалить"}
        </button>
      </div>
    </Dialog>
  );
}
