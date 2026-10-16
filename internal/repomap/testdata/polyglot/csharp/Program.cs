namespace Bank
{
    public class Program : IAuditable
    {
        public static void Main()
        {
            var ledger = new Ledger();
            ledger.Post(new LedgerEntry());
        }

        public void Audit()
        {
        }
    }
}
