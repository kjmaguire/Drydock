namespace Bank
{
    public class Ledger
    {
        public void Post(LedgerEntry entry)
        {
        }
    }

    public struct LedgerEntry
    {
        public int Amount;
    }

    public enum EntryKind
    {
        Credit,
        Debit,
    }

    public interface IAuditable
    {
        void Audit();
    }
}
