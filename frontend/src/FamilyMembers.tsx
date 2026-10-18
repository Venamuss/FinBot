import { useState } from "react";
import type { FormEvent } from "react";
import { api, ApiError, errorMessage } from "./api";
import type { Member } from "./api";
import Dialog from "./Dialog";
import { validID } from "./validation";

function MemberNameEditor({
  member,
  onClose,
  onSaved,
}: {
  member?: Member;
  onClose: () => void;
  onSaved: (member: Member) => void;
}) {
  const [userId, setUserId] = useState(member?.user_id ?? "");
  const [name, setName] = useState(member?.display_name ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [fields, setFields] = useState<Record<string, string>>({});

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (busy) {
      return;
    }

    const displayName = name.trim();
    const validation: Record<string, string> = {};
    if (!validID(userId)) {
      validation.user_id =
        "Укажите Telegram ID от 1 до 9223372036854775807 без ведущих нулей.";
    }

    if ([...displayName].length > 100 || /\p{Cc}/u.test(displayName)) {
      validation.display_name =
        "Имя должно содержать не более 100 символов без управляющих символов.";
    }

    setFields(validation);
    setError("");
    if (Object.keys(validation).length) {
      return;
    }

    setBusy(true);

    try {
      const saved = await api.setMemberName(userId, displayName || null);
      onSaved(saved);
    } catch (error) {
      setError(errorMessage(error));
      if (error instanceof ApiError) {
        setFields(error.fields);
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      title={member ? "Имя участника" : "Добавить участника"}
      busy={busy}
      onClose={onClose}
    >
      <form onSubmit={submit} noValidate>
        {error && (
          <p role="alert" className="notice error">
            {error}
          </p>
        )}
        <fieldset disabled={busy} className="member-form">
          {member ? (
            <p className="muted">
              Telegram ID: <strong>{member.user_id}</strong>
            </p>
          ) : (
            <div className="field">
              <label htmlFor="member-user-id">Telegram ID *</label>
              <input
                id="member-user-id"
                inputMode="numeric"
                value={userId}
                onChange={(event) => setUserId(event.target.value)}
                aria-invalid={Boolean(fields.user_id)}
                aria-describedby={
                  fields.user_id ? "member-id-error" : undefined
                }
                required
                data-autofocus
              />
              {fields.user_id && (
                <span id="member-id-error" className="field-error" role="alert">
                  {fields.user_id}
                </span>
              )}
            </div>
          )}
          <div className="field">
            <label htmlFor="member-name">Отображаемое имя</label>
            <input
              id="member-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              aria-invalid={Boolean(fields.display_name)}
              aria-describedby="member-name-hint member-name-error"
              data-autofocus={member ? true : undefined}
            />
            <span id="member-name-hint" className="hint">
              До 100 символов. Пустое поле возвращает отображение Telegram ID.
            </span>
            <span
              id="member-name-error"
              className="field-error"
              role={fields.display_name ? "alert" : undefined}
            >
              {fields.display_name}
            </span>
          </div>
        </fieldset>
        <div className="dialog-actions">
          <button type="button" onClick={onClose} disabled={busy}>
            Отмена
          </button>
          <button type="submit" className="primary" disabled={busy}>
            {busy ? "Сохраняем…" : "Сохранить имя"}
          </button>
        </div>
      </form>
    </Dialog>
  );
}

export default function FamilyMembers({
  members,
  loading,
  failed,
  onReload,
  onSaved,
}: {
  members: Member[];
  loading: boolean;
  failed: boolean;
  onReload: () => void;
  onSaved: (member: Member) => void;
}) {
  const [editor, setEditor] = useState<{ member?: Member }>();
  return (
    <details className="family-members">
      <summary>
        Участники семьи <span className="count">{members.length}</span>
      </summary>
      <div className="members-content">
        <p className="muted">
          Задайте понятные имена. Расходы и доступ бота по-прежнему связаны с
          Telegram ID.
        </p>
        <div className="members-actions">
          <button onClick={() => setEditor({})}>Добавить участника</button>
          <button className="quiet" onClick={onReload} disabled={loading}>
            Обновить участников
          </button>
        </div>
        {loading ? (
          <p role="status">Загружаем участников…</p>
        ) : failed ? (
          <p className="muted">
            Список участников недоступен. Повторите загрузку.
          </p>
        ) : members.length === 0 ? (
          <p className="muted">
            Пока нет участников. Добавьте ID или создайте первый расход.
          </p>
        ) : (
          <ul className="member-list">
            {members.map((member) => (
              <li key={member.user_id}>
                <div>
                  <span className="member-name">
                    {member.display_name || member.user_id}
                  </span>
                  {member.display_name && (
                    <small className="muted member-secondary">
                      Telegram ID: {member.user_id}
                    </small>
                  )}
                </div>
                <button
                  className="quiet"
                  aria-label={`Изменить имя участника ${member.user_id}`}
                  onClick={() => setEditor({ member })}
                >
                  Изменить имя
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
      {editor && (
        <MemberNameEditor
          member={editor.member}
          onClose={() => setEditor(undefined)}
          onSaved={(member) => {
            setEditor(undefined);
            onSaved(member);
          }}
        />
      )}
    </details>
  );
}
