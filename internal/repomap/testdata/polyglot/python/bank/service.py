from bank.models import Account, open_account


class SavingsAccount(Account):
    def add_interest(self, rate):
        self.deposit(self.balance * rate)


def onboard(owner):
    acct = open_account(owner)
    acct.deposit(10)
    return acct
