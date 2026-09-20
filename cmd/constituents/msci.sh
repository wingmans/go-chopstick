

# curl  --user "$MSCI_USER:$MSCI_PASSWORD" \
#    --request POST \
#   --header 'Content-Type: application/x-www-form-urlencoded' \
#   --data-urlencode 'index_code_list=990100' \
#   --data '' \
#   'https://api2.msci.com/index/constituents/v2.0/indexes/990100/initial?calc_date=20260911&as_of_date=20260911' 


curl --request POST \
     --user "$MSCI_USER:$MSCI_PASSWORD" \
     --url 'https://api.msci.com/index/constituents/v2.0/indexes/990100/full?calc_date=20210405&as_of_date=20210405' \
     --header 'content-type: application/x-www-form-urlencoded' 
     --data index_code_list= -vv
