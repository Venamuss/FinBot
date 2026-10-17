import { useState } from "react";
import type { ChangeEvent, FormEvent } from "react";
import { api, ApiError, categories, errorMessage } from "./api";
import type { Expense, Member } from "./api";
import { changedFields, formInput, today, validateForm } from "./validation";
import type { FormValues } from "./validation";

export default function ExpenseForm({
  original,
  members,
  onSaved,
  onCancel,
  onBusy,
}: {
  original?: Expense;
  members: Member[];
  onSaved: (message: string) => void;
  onCancel: () => void;
  onBusy: (busy: boolean) => void;
}) {
  const [values, setValues] = useState<FormValues>({
    user_id: original?.user_id ?? "",
    amount: original?.amount ?? "",
    category: original?.category ?? "",
    spent_at: original?.spent_at ?? today(),
    comment: original?.comment ?? "",
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [message, setMessage] = useState("");
  const [saving, setSaving] = useState(false);

  function change(field: keyof FormValues, value: string) {
    setValues((current) => ({ ...current, [field]: value }));
    setErrors((current) => ({ ...current, [field]: "" }));
    setMessage("");
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (saving) return;
    const validation = validateForm(values);
    setErrors(validation);
    if (Object.keys(validation).length > 0) {
      setMessage("Проверьте поля формы.");
      return;
    }
    const input = formInput(values, original);
    const patch = original ? changedFields(input, original) : undefined;
    if (patch && Object.keys(patch).length === 0) {
      setMessage("Нет изменений для сохранения.");
      return;
    }
    setSaving(true);
    onBusy(true);
    setMessage("");
    try {
      if (original && patch) await api.update(original.id, patch);
      else await api.create(input);
      onSaved(original ? "Изменения сохранены." : "Расход добавлен.");
    } catch (error) {
      setMessage(errorMessage(error));
      if (error instanceof ApiError) setErrors(error.fields);
    } finally {
      setSaving(false);
      onBusy(false);
    }
  }

  const fieldProps = (field: keyof FormValues) => ({
    id: field,
    name: field,
    value: values[field],
    "aria-invalid": Boolean(errors[field]),
    "aria-describedby": errors[field] ? `${field}-error` : undefined,
    onChange: (
      event: ChangeEvent<
        HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement
      >,
    ) => change(field, event.target.value),
  });
  const fieldError = (field: keyof FormValues) =>
    errors[field] ? (
      <span className="field-error" id={`${field}-error`}>
        {errors[field]}
      </span>
    ) : null;

  return (
    <form onSubmit={submit} noValidate>
      <p className="muted">
        Обязательные поля отмечены *. Участник определяется по Telegram ID.
      </p>
      {message && (
        <p className="notice error" role="alert">
          {message}
        </p>
      )}
      <fieldset disabled={saving}>
        <div className="form-grid">
          <div className="field full">
            <label htmlFor="user_id">Участник · Telegram ID *</label>
            <input
              {...fieldProps("user_id")}
              inputMode="numeric"
              list="member-suggestions"
              required
              data-autofocus
            />
            <datalist id="member-suggestions">
              {members.map((member) => (
                <option key={member.user_id} value={member.user_id} />
              ))}
            </datalist>
            <span className="hint">
              Выберите существующий ID или введите новый.
            </span>
            {fieldError("user_id")}
          </div>
          <div className="field">
            <label htmlFor="amount">Сумма *</label>
            <input
              {...fieldProps("amount")}
              inputMode="decimal"
              placeholder="350.50"
              required
            />
            {fieldError("amount")}
          </div>
          <div className="field">
            <label htmlFor="spent_at">Дата расхода *</label>
            <input
              {...fieldProps("spent_at")}
              type="date"
              min="0001-01-01"
              max="9999-12-31"
              required
            />
            {fieldError("spent_at")}
          </div>
          <div className="field full">
            <label htmlFor="category">Категория *</label>
            <select {...fieldProps("category")} required>
              <option value="">Выберите категорию</option>
              {categories.map((category) => (
                <option key={category} value={category}>
                  {category}
                </option>
              ))}
            </select>
            {fieldError("category")}
          </div>
          <div className="field full">
            <label htmlFor="comment">Комментарий</label>
            <textarea {...fieldProps("comment")} rows={3} />
            <span className="hint">
              Оставьте пустым, чтобы убрать комментарий.
            </span>
            {fieldError("comment")}
          </div>
        </div>
      </fieldset>
      <div className="dialog-actions">
        <button type="button" onClick={onCancel} disabled={saving}>
          Отмена
        </button>
        <button className="primary" type="submit" disabled={saving}>
          {saving ? "Сохраняем…" : original ? "Сохранить" : "Добавить расход"}
        </button>
      </div>
    </form>
  );
}
