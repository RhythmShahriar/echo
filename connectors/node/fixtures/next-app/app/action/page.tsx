import { notifyTeam } from './actions';

export default function ActionPage() {
  return (
    <form
      action={async () => {
        'use server';
        await notifyTeam();
      }}
    >
      <button type="submit">Notify</button>
    </form>
  );
}
